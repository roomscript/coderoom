package interpreter

import (
	"testing"

	"github.com/roomscript/coderoom/internal/session"
)

func TestSessionEventInbox_coalescesWakeupsWhileDrainIsPending(t *testing.T) {
	inbox := &sessionEventInbox{}
	first := session.AgentStarted{Alias: "ada"}
	second := session.AgentStarted{Alias: "turing"}

	if !inbox.Record(first) {
		t.Fatal("first event did not request a drain")
	}
	if inbox.Record(second) {
		t.Fatal("second event requested a duplicate drain")
	}
	burst := inbox.Take()
	if len(burst) != 2 || burst[0] != first || burst[1] != second {
		t.Fatalf("burst = %#v, want first and second in order", burst)
	}
	if inbox.Record(session.AgentStarted{Alias: "grace"}) {
		t.Fatal("event requested a drain before pending drain completed")
	}
}

func TestSessionEventInbox_completeDrainRequiresEmptyInbox(t *testing.T) {
	inbox := &sessionEventInbox{}
	if !inbox.Record(session.AgentStarted{Alias: "ada"}) {
		t.Fatal("first event did not request a drain")
	}
	if burst := inbox.Take(); len(burst) != 1 {
		t.Fatalf("first burst = %#v, want one event", burst)
	}
	if inbox.Record(session.AgentStarted{Alias: "grace"}) {
		t.Fatal("event requested a drain before pending drain observed empty")
	}
	if inbox.CompleteDrain() {
		t.Fatal("drain completed while an event was buffered")
	}
	if burst := inbox.Take(); len(burst) != 1 {
		t.Fatalf("second burst = %#v, want one event", burst)
	}
	if !inbox.CompleteDrain() {
		t.Fatal("drain did not complete after inbox became empty")
	}
	if !inbox.Record(session.AgentStarted{Alias: "linus"}) {
		t.Fatal("event did not request a drain after empty observation")
	}
}

func TestSessionEventInbox_takePreservesPendingDrain(t *testing.T) {
	inbox := &sessionEventInbox{}
	if !inbox.Record(session.AgentStarted{Alias: "ada"}) {
		t.Fatal("first event did not request a drain")
	}
	if burst := inbox.Take(); len(burst) != 1 {
		t.Fatalf("burst = %#v, want one event", burst)
	}
	if inbox.Record(session.AgentStarted{Alias: "turing"}) {
		t.Fatal("causal take cleared the pending drain marker")
	}
}
