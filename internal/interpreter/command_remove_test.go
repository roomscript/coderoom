package interpreter

import (
	"errors"
	"testing"

	"github.com/trigosec/coderoom/internal/session"
)

func TestRemove_executesNativeHandlerOnce(t *testing.T) {
	interp, sess, events := newSubmitContractInterpreter(t)

	mustSubmit(t, interp.Submit("/remove ada"))
	receiveSubmitEvent[InputAccepted](t, events)
	command := receiveSubmitCommand(t, sess.executed)
	if command != (session.RemoveCommand{Alias: "ada"}) {
		t.Fatalf("command = %#v, want remove ada", command)
	}
	receiveSubmitEvent[StateChanged](t, events)
	receiveSubmitEvent[SubmissionSucceeded](t, events)
	assertNoSubmitEvent(t, events)
}

func TestRemove_reportsExecutionFailure(t *testing.T) {
	wantErr := errors.New("remove failed")
	interp, sess, events := newSubmitContractInterpreter(t)
	sess.executeErr = wantErr

	mustSubmit(t, interp.Submit("/remove ada"))
	receiveSubmitEvent[InputAccepted](t, events)
	receiveSubmitCommand(t, sess.executed)
	receiveSubmitEvent[StateChanged](t, events)
	failure := receiveSubmitEvent[SubmissionFailed](t, events)
	if failure.Raw != "/remove ada" || failure.Operation != "remove" || !errors.Is(failure.Err, wantErr) {
		t.Fatalf("failure = %#v", failure)
	}
	assertNoSubmitEvent(t, events)
}
