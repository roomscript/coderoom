package interpreter

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/roomscript/coderoom/internal/agent"
	"github.com/roomscript/coderoom/internal/interpreter/std"
	"github.com/roomscript/coderoom/internal/promptlang"
	"github.com/roomscript/coderoom/internal/session"
	"github.com/roomscript/coderoom/internal/shell"
)

func TestShell_FacadePublishesCompletion(t *testing.T) {
	failure := errors.New("execution failed")
	exitCode := 1
	tests := []struct {
		name   string
		result shell.Result
	}{
		{"success", shell.Result{Status: shell.StatusSuccess, Stdout: "out"}},
		{"failure", shell.Result{Status: shell.StatusFailure, Stderr: "err", ExitCode: &exitCode, Err: failure}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			events := make(chan Event, 64)
			calls := make(chan string, 2)
			runner := ShellRunnerFunc(func(_ context.Context, cwd, program string) shell.Result {
				calls <- cwd + ":" + program
				return tt.result
			})
			interp := New(t.Context(), session.New(), "/workspace", WithShellRunner(runner), WithObserver(routingObserver{events}))
			t.Cleanup(interp.Close)
			parsed, err := promptlang.Parse("/shell echo hello")
			if err != nil {
				t.Fatal(err)
			}
			assertFacadeShellSelection(t, interp, parsed)
			if err := interp.Submit(parsed.Raw); err != nil {
				t.Fatal(err)
			}
			completed := awaitShellCompletion(t, events)
			interp.Close()
			if len(calls) != 1 || <-calls != "/workspace:echo hello" {
				t.Fatal("shell did not execute once with its program and working directory")
			}
			if completed.Command != "echo hello" || completed.Statement.Raw != parsed.Raw || !errors.Is(completed.Result.Err, tt.result.Err) {
				t.Fatalf("completion = %#v", completed)
			}
			assertShellRecord(t, interp, completed)
			assertNoShellOutcome(t, events)
		})
	}
}

func awaitShellCompletion(t *testing.T, events <-chan Event) ShellCompleted {
	t.Helper()
	acknowledged := false
	timeout := time.After(2 * time.Second)
	for {
		select {
		case value := <-events:
			switch event := value.(type) {
			case SubmissionSucceeded:
				if acknowledged {
					t.Fatal("duplicate launch acknowledgement")
				}
				acknowledged = true
			case ShellCompleted:
				if !acknowledged {
					t.Fatal("completion preceded launch acknowledgement")
				}
				return event
			case SubmissionFailed, OperationFailed:
				t.Fatalf("unexpected failure: %#v", event)
			}
		case <-timeout:
			t.Fatal("shell completion timed out")
		}
	}
}

func assertShellRecord(t *testing.T, interp *Interpreter, completed ShellCompleted) {
	t.Helper()
	records := interp.Snapshot().Room.Records
	if len(records) != 2 {
		t.Fatalf("records = %#v; want input and command", records)
	}
	command, ok := records[1].Msg.Content.(agent.Command)
	if !ok || command.Command != completed.Command || command.Cwd != completed.Cwd || command.Output != std.FormatShellResult(completed.Result) || !reflect.DeepEqual(command.ExitCode, completed.Result.ExitCode) {
		t.Fatalf("command record = %#v", records[1])
	}
}

func TestShell_CloseCancelsAndJoinsWorker(t *testing.T) {
	events := make(chan Event, 64)
	started, cancelled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	runner := ShellRunnerFunc(func(ctx context.Context, _, _ string) shell.Result {
		close(started)
		<-ctx.Done()
		close(cancelled)
		<-release
		return shell.Result{Status: shell.StatusCancelled, Err: ctx.Err()}
	})
	interp := New(t.Context(), session.New(), "/workspace", WithShellRunner(runner), WithObserver(routingObserver{events}))
	t.Cleanup(interp.Close)
	var releaseOnce sync.Once
	releaseWorker := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(releaseWorker)
	if err := interp.Submit("/shell blocked"); err != nil {
		t.Fatal(err)
	}
	awaitShellSignal(t, started)
	closed := make(chan struct{})
	go func() { interp.Close(); close(closed) }()
	awaitShellSignal(t, cancelled)
	select {
	case <-closed:
		t.Fatal("Close returned before the worker finished")
	case <-time.After(25 * time.Millisecond):
	}
	releaseWorker()
	awaitShellSignal(t, closed)
	completed := awaitShellCompletion(t, events)
	if completed.Result.Status != shell.StatusCancelled || !errors.Is(completed.Result.Err, context.Canceled) {
		t.Fatalf("completion = %#v", completed)
	}
	assertShellRecord(t, interp, completed)
}

func awaitShellSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(2 * time.Second):
		t.Fatal("shell worker timed out")
	}
}

func assertFacadeShellSelection(t *testing.T, interp *Interpreter, parsed promptlang.ParsedStatement) {
	t.Helper()
	command, ok := interp.commands.Lookup(parsed)
	if _, shellCommand := command.(std.ShellCommand); !ok || !shellCommand {
		t.Fatal("shell is not selected by the facade registry")
	}
}

func assertNoShellOutcome(t *testing.T, events <-chan Event) {
	t.Helper()
	for len(events) != 0 {
		switch event := (<-events).(type) {
		case ShellCompleted, SubmissionSucceeded, SubmissionFailed, OperationFailed:
			t.Fatalf("unexpected additional outcome: %#v", event)
		}
	}
}
