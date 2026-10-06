package interpreter

import (
	"errors"
	"testing"

	"github.com/roomscript/coderoom/internal/session"
)

func TestInvite_executesNativeHandlerOnce(t *testing.T) {
	interp, sess, events := newSubmitContractInterpreter(t)

	mustSubmit(t, interp.Submit("/invite ada"))
	receiveSubmitEvent[InputAccepted](t, events)
	command := receiveSubmitCommand(t, sess.executed)
	if command != (session.InviteCommand{Alias: "ada"}) {
		t.Fatalf("command = %#v, want invite ada", command)
	}
	receiveSubmitEvent[StateChanged](t, events)
	receiveSubmitEvent[SubmissionSucceeded](t, events)
	assertNoSubmitEvent(t, events)
}

func TestInvite_reportsExecutionFailure(t *testing.T) {
	wantErr := errors.New("invite failed")
	interp, sess, events := newSubmitContractInterpreter(t)
	sess.executeErr = wantErr

	mustSubmit(t, interp.Submit("/invite ada"))
	receiveSubmitEvent[InputAccepted](t, events)
	receiveSubmitCommand(t, sess.executed)
	receiveSubmitEvent[StateChanged](t, events)
	failure := receiveSubmitEvent[SubmissionFailed](t, events)
	if failure.Raw != "/invite ada" || failure.Operation != "invite" || !errors.Is(failure.Err, wantErr) {
		t.Fatalf("failure = %#v", failure)
	}
	assertNoSubmitEvent(t, events)
}
