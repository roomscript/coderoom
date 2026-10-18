package monolith

import (
	"sync"
	"testing"

	"github.com/roomscript/coderoom/internal/session"
)

func TestSessionEventInbox_coalescesWakeupsWhileDrainIsPending(t *testing.T) {
	inbox := &sessionEventInbox{}
	first := session.AgentReady{Alias: testAlias("ada")}
	second := session.AgentReady{Alias: testAlias("turing")}

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
	if inbox.Record(session.AgentReady{Alias: testAlias("grace")}) {
		t.Fatal("event requested a drain before pending drain completed")
	}
}

func TestSessionEventInbox_completeDrainRequiresEmptyInbox(t *testing.T) {
	inbox := &sessionEventInbox{}
	if !inbox.Record(session.AgentReady{Alias: testAlias("ada")}) {
		t.Fatal("first event did not request a drain")
	}
	if burst := inbox.Take(); len(burst) != 1 {
		t.Fatalf("first burst = %#v, want one event", burst)
	}
	if inbox.Record(session.AgentReady{Alias: testAlias("grace")}) {
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
	if !inbox.Record(session.AgentReady{Alias: testAlias("linus")}) {
		t.Fatal("event did not request a drain after empty observation")
	}
}

func TestSessionEventInbox_takePreservesPendingDrain(t *testing.T) {
	inbox := &sessionEventInbox{}
	if !inbox.Record(session.AgentReady{Alias: testAlias("ada")}) {
		t.Fatal("first event did not request a drain")
	}
	if burst := inbox.Take(); len(burst) != 1 {
		t.Fatalf("burst = %#v, want one event", burst)
	}
	if inbox.Record(session.AgentReady{Alias: testAlias("turing")}) {
		t.Fatal("causal take cleared the pending drain marker")
	}
}

func TestSessionEventInbox_executorDrainsCoalescedWakeups(t *testing.T) {
	interp, sess, _ := newSubmitContractInterpreter(t)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	mustSubmit(t, interp.Schedule(func() { close(entered); <-release }))
	receiveSignal(t, entered, "blocked lane")
	sess.mu.Lock()
	observer := sess.observer
	sess.mu.Unlock()
	for id := range 1000 {
		observer.OnEvent(session.ApprovalCleared{ID: int64(id + 1)})
	}
	drained := make(chan struct{})
	mustSubmit(t, interp.Schedule(func() { close(drained) }))
	unblock()
	receiveSignal(t, drained, "event drain")
	interp.executor.inbox.mu.Lock()
	pending := interp.executor.inbox.drainPending
	remaining := len(interp.executor.inbox.events)
	interp.executor.inbox.mu.Unlock()
	if pending || remaining != 0 {
		t.Fatalf("inbox pending=%v remaining=%d", pending, remaining)
	}
}
