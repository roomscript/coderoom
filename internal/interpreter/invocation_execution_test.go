package interpreter

import (
	"errors"
	"testing"

	"github.com/roomscript/coderoom/internal/interpreter/runtime"
	"github.com/roomscript/coderoom/internal/room"
)

type callbackTestCommand struct {
	callbacks chan func(runtime.Completion)
	launchErr error
	immediate *runtime.Completion
	entered   chan struct{}
	release   chan struct{}
}

func (*callbackTestCommand) Name() string        { return "test" }
func (*callbackTestCommand) Usage() string       { return "/test" }
func (*callbackTestCommand) Description() string { return "callback test" }
func (c *callbackTestCommand) Prepare(runtime.Context) (runtime.Invocation, error) {
	return c, nil
}

func (c *callbackTestCommand) Go(complete func(runtime.Completion)) error {
	c.callbacks <- complete
	if c.immediate != nil {
		complete(*c.immediate)
	}
	if c.entered != nil {
		close(c.entered)
		<-c.release
	}
	return c.launchErr
}

type callbackTestOperation struct{ command *callbackTestCommand }

func (op callbackTestOperation) apply(e *interpreterExecutor) {
	e.runner.Run(append(acceptedInputSequence("/test"), goInvocationInstruction{
		command: op.command,
		outcome: submissionOutcome{raw: "/test", operation: "test"},
	}))
}

func enqueueCallbackTest(t *testing.T, interp *Interpreter, c *callbackTestCommand) {
	t.Helper()
	if !interp.executor.enqueue(callbackTestOperation{command: c}) {
		t.Fatal("could not enqueue test invocation")
	}
}

func TestInvocationCompletion_isQueuedUntilLaunchReturns(t *testing.T) {
	interp, _, events := newSubmitContractInterpreter(t)
	completion := runtime.Completion{Records: []room.Record{{Kind: room.KindSystem, Text: "finished"}}}
	c := &callbackTestCommand{
		callbacks: make(chan func(runtime.Completion), 1), immediate: &completion,
		entered: make(chan struct{}), release: make(chan struct{}),
	}
	t.Cleanup(func() {
		select {
		case <-c.release:
		default:
			close(c.release)
		}
	})
	enqueueCallbackTest(t, interp, c)
	<-c.entered
	receiveSubmitEvent[InputAccepted](t, events)
	assertNoSubmitEvent(t, events)
	close(c.release)
	receiveSubmitEvent[StateChanged](t, events)
	receiveSubmitEvent[SubmissionSucceeded](t, events)
	if records := interp.Snapshot().Room.Records; len(records) != 2 || records[1].Text != "finished" {
		t.Fatalf("records = %#v, want input and completion", records)
	}
}

func TestInvocationCompletion_delayedAndDuplicate(t *testing.T) {
	interp, _, events := newSubmitContractInterpreter(t)
	c := &callbackTestCommand{callbacks: make(chan func(runtime.Completion), 1)}
	enqueueCallbackTest(t, interp, c)
	complete := <-c.callbacks
	receiveSubmitEvent[InputAccepted](t, events)
	// A pending invocation does not block another submission.
	mustSubmit(t, interp.Submit("/who"))
	receiveSubmitEvent[InputAccepted](t, events)
	receiveSubmitEvent[StateChanged](t, events)
	receiveSubmitEvent[SubmissionSucceeded](t, events)
	completion := runtime.Completion{Records: []room.Record{{Kind: room.KindSystem, Text: "finished"}}}
	complete(completion)
	complete(completion)
	receiveSubmitEvent[StateChanged](t, events)
	if result := receiveSubmitEvent[SubmissionSucceeded](t, events); result.Raw != "/test" {
		t.Fatalf("completion raw = %q, want /test", result.Raw)
	}
	if records := interp.Snapshot().Room.Records; len(records) != 4 || records[3].Text != "finished" {
		t.Fatalf("records = %#v, want one delayed completion", records)
	}
	assertNoSubmitEvent(t, events)
}

func TestInvocationCompletion_launchFailureIgnoresQueuedCallback(t *testing.T) {
	interp, _, events := newSubmitContractInterpreter(t)
	completion := runtime.Completion{Records: []room.Record{{Kind: room.KindSystem, Text: "discard"}}}
	c := &callbackTestCommand{
		callbacks: make(chan func(runtime.Completion), 1), immediate: &completion,
		launchErr: errors.New("launch failed"),
	}
	enqueueCallbackTest(t, interp, c)
	receiveSubmitEvent[InputAccepted](t, events)
	receiveSubmitEvent[StateChanged](t, events)
	receiveSubmitEvent[SubmissionFailed](t, events)
	if records := interp.Snapshot().Room.Records; len(records) != 1 {
		t.Fatalf("records = %#v, want only input", records)
	}
	assertNoSubmitEvent(t, events)
}

func TestInvocationCompletion_shutdownSettlesPendingAndRejectsLateCallback(t *testing.T) {
	interp, _, events := newSubmitContractInterpreter(t)
	c := &callbackTestCommand{callbacks: make(chan func(runtime.Completion), 1)}
	enqueueCallbackTest(t, interp, c)
	complete := <-c.callbacks
	receiveSubmitEvent[InputAccepted](t, events)
	interp.Close()
	receiveSubmitEvent[StateChanged](t, events)
	failed := receiveSubmitEvent[SubmissionFailed](t, events)
	if !errors.Is(failed.Err, ErrClosed) {
		t.Fatalf("completion error = %v, want ErrClosed", failed.Err)
	}
	complete(runtime.Completion{Records: []room.Record{{Kind: room.KindSystem, Text: "late"}}})
	if records := interp.Snapshot().Room.Records; len(records) != 1 {
		t.Fatalf("records = %#v, want only input", records)
	}
	assertNoSubmitEvent(t, events)
}
