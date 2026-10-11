package runtime_test

import (
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/roomscript/coderoom/internal/agent"
	"github.com/roomscript/coderoom/internal/interpreter/runtime"
	"github.com/roomscript/coderoom/internal/interpreter/std"
	"github.com/roomscript/coderoom/internal/promptlang"
	"github.com/roomscript/coderoom/internal/shell"
)

// prototypeInterpreter demonstrates host code without wiring the real interpreter.
// Only its queue is accessed by workers. Publication is recorded for assertions.
type prototypeInterpreter struct {
	queue        chan queuedInvocationCompletion
	pending      map[*runtime.InvocationLifecycle]struct{}
	publications []string
	completions  []runtime.CommandResult
}

type queuedInvocationCompletion struct {
	lifecycle  *runtime.InvocationLifecycle
	completion runtime.CommandResult
}

func newPrototypeInterpreter() *prototypeInterpreter {
	return &prototypeInterpreter{
		queue:   make(chan queuedInvocationCompletion, 8),
		pending: make(map[*runtime.InvocationLifecycle]struct{}),
	}
}

// submit runs on the serialized path. Preparation remains a command responsibility.
func (h *prototypeInterpreter) submit(command runtime.Command, statement promptlang.ParsedStatement, ctx runtime.Context) error {
	invocation, err := command.Prepare(statement, ctx)
	if err != nil {
		h.publications = append(h.publications, "submission failed")
		return fmt.Errorf("prepare command: %w", err)
	}
	lifecycle := runtime.NewInvocationLifecycle(invocation)
	h.pending[lifecycle] = struct{}{}
	err = lifecycle.Start(func(completion runtime.CommandResult) {
		// Capture the lifecycle to correlate this callback with its own invocation.
		h.queue <- queuedInvocationCompletion{lifecycle: lifecycle, completion: completion}
	})
	if err != nil {
		delete(h.pending, lifecycle)
		h.publications = append(h.publications, "submission failed")
		return fmt.Errorf("start invocation: %w", err)
	}
	h.publications = append(h.publications, "submission succeeded")
	return nil
}

// apply runs later on the serialized path, after submission acknowledgement.
func (h *prototypeInterpreter) apply(op queuedInvocationCompletion) {
	completion, ok := op.lifecycle.Complete(op.completion)
	if !ok {
		return
	}
	delete(h.pending, op.lifecycle)
	h.publishCompletion(completion)
}

func (h *prototypeInterpreter) publishCompletion(completion runtime.CommandResult) {
	// The real interpreter appends records, publishes execution errors and snapshots.
	h.completions = append(h.completions, completion)
	h.publications = append(h.publications, "completion")
}

func (h *prototypeInterpreter) close(reason error) {
	// The real host first closes admission, drains accepted operations, cancels and
	// joins workers, then drains the completion queue. This fixture starts afterward.
	for len(h.queue) > 0 {
		h.apply(<-h.queue)
	}
	for lifecycle := range h.pending {
		if completion, ok := lifecycle.Close(reason); ok {
			h.publishCompletion(completion)
		}
		delete(h.pending, lifecycle)
	}
}

func TestInvocationLifecycle_InterpreterWhoFlow(t *testing.T) {
	host := newPrototypeInterpreter()
	err := host.submit(std.WhoCommand{}, promptlang.ParsedStatement{Value: promptlang.Who{}}, runtime.Context{Participants: participantReader{}})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(host.publications, []string{"submission succeeded"}) {
		t.Fatalf("published before processing queue: %#v", host.publications)
	}
	op := <-host.queue
	host.apply(op)
	host.apply(op)
	if len(host.completions) != 1 || host.completions[0].Records[0].Text != "[agents] ada" || len(host.pending) != 0 {
		t.Fatalf("completed = %#v, pending = %d", host.completions, len(host.pending))
	}
	if !reflect.DeepEqual(host.publications, []string{"submission succeeded", "completion"}) {
		t.Fatalf("publication order = %#v", host.publications)
	}
	host.close(errors.New("closed"))
	if len(host.completions) != 1 {
		t.Fatal("shutdown repeated completion")
	}
}

type prototypeShellLauncher struct {
	complete func(shell.Result)
}

func (*prototypeShellLauncher) Cwd() string { return "/workspace" }
func (l *prototypeShellLauncher) Go(_ string, complete func(shell.Result)) error {
	l.complete = complete
	return nil
}

func TestInvocationLifecycle_InterpreterShellFlow(t *testing.T) {
	host := newPrototypeInterpreter()
	launcher := &prototypeShellLauncher{}
	statement := promptlang.ParsedStatement{Value: promptlang.Shell{Program: promptlang.Located[string]{Value: "echo hello"}}}
	if err := host.submit(std.ShellCommand{}, statement, runtime.Context{Shell: launcher}); err != nil {
		t.Fatal(err)
	}
	if len(host.queue) != 0 || len(host.pending) != 1 {
		t.Fatal("shell did not remain pending")
	}
	// Complete on a worker: the callback enqueues but cannot publish or change state.
	done := make(chan struct{})
	go func() {
		launcher.complete(shell.Result{Status: shell.StatusSuccess, Stdout: "hello"})
		close(done)
	}()
	<-done
	if len(host.completions) != 0 || len(host.pending) != 1 {
		t.Fatal("worker mutated host lifecycle or publication")
	}
	// Accepted completion survives shutdown because the host drains before closing.
	host.close(errors.New("closed"))
	if len(host.completions) != 1 || host.completions[0].Err != nil {
		t.Fatalf("shell completion = %#v", host.completions)
	}
	assertPrototypeShellCompletion(t, host.completions[0])
}

func assertPrototypeShellCompletion(t *testing.T, completion runtime.CommandResult) {
	t.Helper()
	record := completion.Records[0]
	command, ok := record.Msg.Content.(agent.Command)
	if !ok || command.Command != "echo hello" || command.Cwd != "/workspace" {
		t.Fatalf("shell record = %#v", record)
	}
}

func TestInvocationLifecycle_FailedLaunchAndUnsupportedStaging(t *testing.T) {
	failure := errors.New("launch failed")
	for _, tt := range []struct {
		name       string
		invocation *invocation
		wantErr    error
	}{
		{name: "callback before failed launch", invocation: &invocation{err: failure, immediate: true}, wantErr: failure},
		{name: "unsupported staging", invocation: &invocation{staging: &runtime.Staging{Aliases: []string{"ada"}}}, wantErr: runtime.ErrStagingUnsupported},
	} {
		t.Run(tt.name, func(t *testing.T) {
			host := newPrototypeInterpreter()
			if err := host.submit(&command{invocation: tt.invocation}, promptlang.ParsedStatement{}, runtime.Context{}); !errors.Is(err, tt.wantErr) {
				t.Fatalf("submission error = %v", err)
			}
			host.close(errors.New("closed"))
			if len(host.completions) != 0 || len(host.pending) != 0 {
				t.Fatal("failed submission produced an execution outcome")
			}
		})
	}
}

func TestInvocationLifecycle_StartOnceAndClose(t *testing.T) {
	i := &invocation{}
	lifecycle := runtime.NewInvocationLifecycle(i)
	if _, ok := lifecycle.Complete(runtime.CommandResult{}); ok {
		t.Fatal("completion accepted before launch")
	}
	if err := lifecycle.Start(func(runtime.CommandResult) {}); err != nil {
		t.Fatal(err)
	}
	if err := lifecycle.Start(func(runtime.CommandResult) {}); !errors.Is(err, runtime.ErrInvocationNotPrepared) || i.launches != 1 {
		t.Fatalf("repeated launch = %v, launches = %d", err, i.launches)
	}
	reason := errors.New("closed")
	completion, ok := lifecycle.Close(reason)
	if !ok || !errors.Is(completion.Err, reason) {
		t.Fatalf("close = %#v, %v", completion, ok)
	}
	if _, ok := lifecycle.Complete(runtime.CommandResult{}); ok {
		t.Fatal("late completion accepted")
	}
	if _, ok := lifecycle.Close(reason); ok {
		t.Fatal("shutdown repeated outcome")
	}
}
