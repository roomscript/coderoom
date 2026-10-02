package interpreter

import (
	"errors"
	"testing"

	"github.com/trigosec/coderoom/internal/policy"
	"github.com/trigosec/coderoom/internal/session"
)

func TestPolicy_executesNativeHandlerOnce(t *testing.T) {
	interp, sess, events := newSubmitContractInterpreter(t)

	mustSubmit(t, interp.Submit("/policy enable send-notices"))
	receiveSubmitEvent[InputAccepted](t, events)
	command := receiveSubmitCommand(t, sess.executed)
	if command != (session.EnablePolicyCommand{Name: policy.SendNotices}) {
		t.Fatalf("command = %#v, want enable send-notices", command)
	}
	receiveSubmitEvent[StateChanged](t, events)
	receiveSubmitEvent[SubmissionSucceeded](t, events)
	assertNoSubmitEvent(t, events)
}

func TestPolicy_reportsExecutionFailure(t *testing.T) {
	wantErr := errors.New("policy failed")
	interp, sess, events := newSubmitContractInterpreter(t)
	sess.executeErr = wantErr

	mustSubmit(t, interp.Submit("/policy enable echo-invites"))
	receiveSubmitEvent[InputAccepted](t, events)
	receiveSubmitCommand(t, sess.executed)
	receiveSubmitEvent[StateChanged](t, events)
	failure := receiveSubmitEvent[SubmissionFailed](t, events)
	if failure.Raw != "/policy enable echo-invites" || failure.Operation != "policy" || !errors.Is(failure.Err, wantErr) {
		t.Fatalf("failure = %#v", failure)
	}
	assertNoSubmitEvent(t, events)
}
