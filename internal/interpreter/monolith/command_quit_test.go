package monolith

import (
	"errors"
	"testing"
)

func TestSubmitContract_quitRequestsExitAfterTerminalOutcome(t *testing.T) {
	interp, sess, events := newSubmitContractInterpreter(t)

	mustSubmit(t, interp.Submit("/quit"))
	receiveSubmitEvent[InputAccepted](t, events)
	changed := receiveSubmitEvent[StateChanged](t, events)
	if len(changed.Snapshot.Room.Records) != 1 || changed.Snapshot.Room.Records[0].Text != "/quit" {
		t.Fatalf("records = %#v, want /quit input", changed.Snapshot.Room.Records)
	}
	receiveSubmitEvent[SubmissionSucceeded](t, events)
	receiveSubmitEvent[ExitRequested](t, events)
	if shutdowns := sess.shutdowns.Load(); shutdowns != 1 {
		t.Fatalf("session shutdowns = %d, want 1", shutdowns)
	}
	if err := interp.Submit("/who"); !errors.Is(err, ErrClosed) {
		t.Fatalf("submission after quit = %v, want ErrClosed", err)
	}
	assertNoSubmitExecution(t, sess.executed)
	assertNoSubmitEvent(t, events)
}
