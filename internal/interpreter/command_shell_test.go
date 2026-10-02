package interpreter

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/trigosec/coderoom/internal/agent"
	"github.com/trigosec/coderoom/internal/shell"
)

type fakeShellRunner struct {
	mu        sync.Mutex
	calls     []shellCall
	result    shell.Result
	started   chan struct{}
	cancelled chan struct{}
	release   chan struct{}
}

type shellCall struct {
	cwd     string
	program string
}

func (r *fakeShellRunner) Run(ctx context.Context, cwd, program string) shell.Result {
	r.mu.Lock()
	r.calls = append(r.calls, shellCall{cwd: cwd, program: program})
	r.mu.Unlock()
	if r.started != nil {
		close(r.started)
	}
	if r.release != nil {
		select {
		case <-ctx.Done():
			if r.cancelled != nil {
				close(r.cancelled)
			}
			<-r.release
			return shell.Result{Status: shell.StatusCancelled, Err: ctx.Err()}
		case <-r.release:
		}
	}
	return r.result
}

func (r *fakeShellRunner) recordedCalls() []shellCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]shellCall(nil), r.calls...)
}

func TestSubmitContract_shellRunsAndPublishesStructuredCompletion(t *testing.T) {
	runner := &fakeShellRunner{result: shell.Result{
		Status: shell.StatusFailure,
		Stdout: "out",
		Stderr: "err",
		Err:    errors.New("runner failure"),
	}}
	interp, events := newShellTestInterpreter(t, runner)

	mustSubmit(t, interp.Submit("/shell echo hello"))
	receiveSubmitEvent[InputAccepted](t, events)
	receiveSubmitEvent[SubmissionSucceeded](t, events)
	completed := receiveSubmitEvent[ShellCompleted](t, events)
	assertShellCompletion(t, completed)
	changed := receiveSubmitEvent[StateChanged](t, events)
	assertShellCompletionRecord(t, changed, completed)
	assertShellCall(t, runner.recordedCalls(), "/workspace", "echo hello")
}

func assertShellCompletion(t *testing.T, completed ShellCompleted) {
	t.Helper()
	if completed.Command != "echo hello" || completed.Cwd != "/workspace" {
		t.Fatalf("completion = %#v", completed)
	}
	if completed.Result.Status != shell.StatusFailure || completed.Output == "" {
		t.Fatalf("completion result = %#v", completed)
	}
}

func assertShellCompletionRecord(t *testing.T, changed StateChanged, completed ShellCompleted) {
	t.Helper()
	if len(changed.Snapshot.Room.Records) != 2 {
		t.Fatalf("records = %#v, want input and command", changed.Snapshot.Room.Records)
	}
	record := changed.Snapshot.Room.Records[1]
	command, ok := record.Msg.Content.(agent.Command)
	if !ok || command.Command != completed.Command || command.Output != completed.Output {
		t.Fatalf("command record = %#v", record)
	}
}

func assertShellCall(t *testing.T, calls []shellCall, cwd, program string) {
	t.Helper()
	if len(calls) != 1 || calls[0].cwd != cwd || calls[0].program != program {
		t.Fatalf("calls = %#v", calls)
	}
}

func TestSubmitContract_definesAndInvokesShellCommand(t *testing.T) {
	runner := &fakeShellRunner{result: shell.Result{Status: shell.StatusSuccess}}
	interp, events := newShellTestInterpreter(t, runner)

	mustSubmit(t, interp.Submit("/def tests /shell go test ./..."))
	receiveSubmitEvent[InputAccepted](t, events)
	defined := receiveSubmitEvent[StateChanged](t, events)
	if got := defined.Snapshot.Room.Records[1].Text; got != "[defined] /tests" {
		t.Fatalf("definition record = %q", got)
	}
	receiveSubmitEvent[SubmissionSucceeded](t, events)

	mustSubmit(t, interp.Submit("/tests"))
	receiveSubmitEvent[InputAccepted](t, events)
	receiveSubmitEvent[SubmissionSucceeded](t, events)
	completed := receiveSubmitEvent[ShellCompleted](t, events)
	if completed.Command != "/tests" {
		t.Fatalf("command = %q, want /tests", completed.Command)
	}
	receiveSubmitEvent[StateChanged](t, events)
	if calls := runner.recordedCalls(); len(calls) != 1 || calls[0].program != "go test ./..." {
		t.Fatalf("calls = %#v", calls)
	}
}

func TestSubmitContract_duplicateCommandDefinitionFails(t *testing.T) {
	interp, events := newShellTestInterpreter(t, &fakeShellRunner{})

	mustSubmit(t, interp.Submit("/def tests /shell true"))
	receiveSubmitEvent[InputAccepted](t, events)
	receiveSubmitEvent[StateChanged](t, events)
	receiveSubmitEvent[SubmissionSucceeded](t, events)

	mustSubmit(t, interp.Submit("/def tests /shell false"))
	receiveSubmitEvent[InputAccepted](t, events)
	failed := receiveSubmitEvent[SubmissionFailed](t, events)
	if failed.Operation == "" || failed.Code != ErrorCommandExists || failed.Err == nil {
		t.Fatalf("failure = %#v", failed)
	}
	assertNoSubmitEvent(t, events)
}

func TestSubmitContract_reservedCommandDefinitionFails(t *testing.T) {
	interp, events := newShellTestInterpreter(t, &fakeShellRunner{})

	mustSubmit(t, interp.Submit("/def help /shell true"))
	receiveSubmitEvent[InputAccepted](t, events)
	failed := receiveSubmitEvent[SubmissionFailed](t, events)
	if failed.Operation == "" || failed.Code != ErrorReservedCommand || failed.Err == nil {
		t.Fatalf("failure = %#v", failed)
	}
	assertNoSubmitEvent(t, events)
}

func TestFormatShellResult_keepsStatusOnlyOutputCompact(t *testing.T) {
	got := formatShellResult(shell.Result{Status: shell.StatusFailure})
	if got != "status: failure" {
		t.Errorf("formatShellResult = %q, want compact status", got)
	}
}

func TestSubmitContract_undefinedInvocationIsUnknown(t *testing.T) {
	interp, events := newShellTestInterpreter(t, &fakeShellRunner{})

	mustSubmit(t, interp.Submit("/tests"))
	unknown := receiveSubmitEvent[UnknownCommand](t, events)
	if unknown.Name != "tests" {
		t.Fatalf("unknown = %#v", unknown)
	}
	assertNoSubmitEvent(t, events)
}

func TestInterpreter_closeCancelsAndWaitsForShell(t *testing.T) {
	runner := &fakeShellRunner{
		started:   make(chan struct{}),
		cancelled: make(chan struct{}),
		release:   make(chan struct{}),
	}
	interp, events := newShellTestInterpreter(t, runner)
	mustSubmit(t, interp.Submit("/shell long-running"))
	receiveSubmitEvent[InputAccepted](t, events)
	receiveSubmitEvent[SubmissionSucceeded](t, events)
	<-runner.started

	closed := make(chan struct{})
	go func() {
		interp.Close()
		close(closed)
	}()
	select {
	case <-runner.cancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not cancel shell runner")
	}
	select {
	case <-closed:
		t.Fatal("Close returned before shell runner completed")
	case <-time.After(25 * time.Millisecond):
	}
	close(runner.release)
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not wait for shell runner")
	}
}

func newShellTestInterpreter(t *testing.T, runner ShellRunner) (*Interpreter, chan Event) {
	t.Helper()
	sess := newSubmitContractSession()
	events := make(chan Event, 16)
	interp := New(context.Background(), sess, "/workspace", WithShellRunner(runner), WithObserver(submitContractObserver{events: events}))
	receiveSubmitEvent[StateChanged](t, events)
	t.Cleanup(interp.Close)
	return interp, events
}
