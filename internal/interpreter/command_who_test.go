package interpreter

import (
	"testing"

	"github.com/trigosec/coderoom/internal/participant"
)

func TestSubmitContract_executesNativeHandlerOnce(t *testing.T) {
	interp, sess, events := newSubmitContractInterpreter(t)
	sess.roster = []participant.View{{Alias: "ada", Status: participant.StatusStarting}}

	mustSubmit(t, interp.Submit("/who"))
	receiveSubmitEvent[InputAccepted](t, events)
	receiveSubmitEvent[StateChanged](t, events)
	roster := receiveSubmitEvent[RosterListed](t, events)
	if len(roster.Participants) != 1 || roster.Participants[0].Alias != "ada" {
		t.Fatalf("roster = %#v, want ada", roster.Participants)
	}
	receiveSubmitEvent[SubmissionSucceeded](t, events)
	assertNoSubmitExecution(t, sess.executed)
	assertNoSubmitEvent(t, events)
}
