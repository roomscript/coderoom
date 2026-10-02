package interpreter

import (
	"reflect"
	"testing"
)

func TestSubmitContract_helpPublishesCommandMetadata(t *testing.T) {
	interp, sess, events := newSubmitContractInterpreter(t)

	mustSubmit(t, interp.Submit("/help"))
	receiveSubmitEvent[InputAccepted](t, events)
	receiveSubmitEvent[StateChanged](t, events)
	help := receiveSubmitEvent[HelpListed](t, events)
	if !reflect.DeepEqual(help, helpListing()) {
		t.Fatalf("help = %#v, want canonical metadata %#v", help, helpListing())
	}
	receiveSubmitEvent[SubmissionSucceeded](t, events)
	assertNoSubmitExecution(t, sess.executed)
	assertNoSubmitEvent(t, events)
}
