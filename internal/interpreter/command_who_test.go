package interpreter

import (
	"testing"

	"github.com/roomscript/coderoom/internal/participant"
)

func TestSubmitContract_executesNativeHandlerOnce(t *testing.T) {
	interp, sess, events := newSubmitContractInterpreter(t)
	sess.participants = []participant.View{{Alias: "ada", Status: participant.StatusStarting}}

	mustSubmit(t, interp.Submit("/who"))
	receiveSubmitEvent[InputAccepted](t, events)
	receiveSubmitEvent[StateChanged](t, events)
	participants := receiveSubmitEvent[ParticipantsListed](t, events)
	if len(participants.Participants) != 1 || participants.Participants[0].Alias != "ada" {
		t.Fatalf("participants = %#v, want ada", participants.Participants)
	}
	receiveSubmitEvent[SubmissionSucceeded](t, events)
	assertNoSubmitExecution(t, sess.executed)
	assertNoSubmitEvent(t, events)
}
