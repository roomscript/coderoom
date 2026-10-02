package interpreter

import (
	"errors"
	"testing"

	"github.com/trigosec/coderoom/internal/session"
)

func TestCancel_executesNativeHandlerOnce(t *testing.T) {
	interp, sess, events := newSubmitContractInterpreter(t)

	mustSubmit(t, interp.Submit("/cancel ada"))
	receiveSubmitEvent[InputAccepted](t, events)
	command := receiveSubmitCommand(t, sess.executed)
	if command != (session.CancelCommand{Alias: "ada"}) {
		t.Fatalf("command = %#v, want cancel ada", command)
	}
	receiveSubmitEvent[StateChanged](t, events)
	receiveSubmitEvent[SubmissionSucceeded](t, events)
	assertNoSubmitEvent(t, events)
}

func TestCancel_reportsExecutionFailure(t *testing.T) {
	wantErr := errors.New("cancel failed")
	interp, sess, events := newSubmitContractInterpreter(t)
	sess.executeErr = wantErr

	mustSubmit(t, interp.Submit("/cancel ada"))
	receiveSubmitEvent[InputAccepted](t, events)
	receiveSubmitCommand(t, sess.executed)
	receiveSubmitEvent[StateChanged](t, events)
	failure := receiveSubmitEvent[SubmissionFailed](t, events)
	if failure.Raw != "/cancel ada" || failure.Operation != "cancel" || !errors.Is(failure.Err, wantErr) {
		t.Fatalf("failure = %#v", failure)
	}
	assertNoSubmitEvent(t, events)
}
