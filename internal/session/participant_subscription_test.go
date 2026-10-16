package session

import (
	"sync"
	"testing"
	"time"

	"github.com/roomscript/coderoom/internal/agent"
	"github.com/roomscript/coderoom/internal/participant"
)

func newReadySubscriptionSession(t *testing.T) (*Session, participant.Alias) {
	t.Helper()
	s := New()
	t.Cleanup(s.Shutdown)
	alias := registerIdleBeforeReadyParticipant(t, s)
	if err := s.updateParticipant(alias, func(p *participant.Participant) (Event, error) {
		return nil, p.SessionReady()
	}); err != nil {
		t.Fatal(err)
	}
	return s, alias
}

func receiveParticipantFact(t *testing.T, facts <-chan Event) Event {
	t.Helper()
	select {
	case event := <-facts:
		return event
	case <-time.After(time.Second):
		t.Fatal("participant fact did not arrive")
		return nil
	}
}

func assertParticipantStatus(t *testing.T, facts <-chan Event, alias participant.Alias, status participant.Status) {
	t.Helper()
	event, ok := receiveParticipantFact(t, facts).(ParticipantStatusChanged)
	if !ok || event.Alias != alias || event.To != status {
		t.Fatalf("participant fact = %#v, want %v / %v", event, alias, status)
	}
}

func startBlockedPreparation(t *testing.T, s *Session, alias participant.Alias, entered <-chan struct{}) <-chan error {
	t.Helper()
	prepared := make(chan error, 1)
	go func() { prepared <- s.prepareParticipantForWork(alias, 1) }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("preparing fact did not reach callback")
	}
	return prepared
}

func TestSubscribeParticipants_snapshotExcludesQueuedEarlierFacts(t *testing.T) {
	s, alias := newReadySubscriptionSession(t)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	facts := make(chan Event, 4)
	initial, unsubscribe := s.SubscribeParticipants(func(event Event) {
		// Session reads are safe inside the callback: delivery holds no Session lock.
		_ = s.Participants()
		if changed, ok := event.(ParticipantStatusChanged); ok && changed.To == participant.StatusPreparing {
			close(entered)
			<-release
		}
		facts <- event
	})
	t.Cleanup(unsubscribe)
	if len(initial.Participants) != 1 || !initial.Participants[0].IsReadyForWork() {
		t.Fatalf("initial snapshot = %#v", initial)
	}
	prepared := startBlockedPreparation(t, s, alias, entered)
	s.beginParticipantWorking(alias, "anchor")
	laterFacts := make(chan Event, 4)
	later, stopLater := s.SubscribeParticipants(func(event Event) { laterFacts <- event })
	t.Cleanup(stopLater)
	if len(later.Participants) != 1 || later.Participants[0].Status != participant.StatusWorking || !initial.Participants[0].IsReadyForWork() {
		t.Fatal("snapshot missed queued working state or mutated initial snapshot")
	}
	unblock()
	if err := <-prepared; err != nil {
		t.Fatal(err)
	}
	assertParticipantStatus(t, facts, alias, participant.StatusPreparing)
	assertParticipantStatus(t, facts, alias, participant.StatusWorking)
	if len(laterFacts) != 0 {
		t.Fatal("new subscription replayed facts from before its snapshot")
	}
	s.abortWork(alias)
	assertParticipantStatus(t, facts, alias, participant.StatusIdle)
	assertParticipantStatus(t, laterFacts, alias, participant.StatusIdle)
}

func TestSubscribeParticipants_unsubscribeFromCallbackDropsQueuedFacts(t *testing.T) {
	s, alias := newReadySubscriptionSession(t)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	facts := make(chan Event, 4)
	var unsubscribe func()
	_, unsubscribe = s.SubscribeParticipants(func(event Event) {
		close(entered)
		<-release
		unsubscribe()
		facts <- event
	})
	t.Cleanup(unsubscribe)
	prepared := startBlockedPreparation(t, s, alias, entered)
	s.beginParticipantWorking(alias, "anchor")
	unblock()
	if err := <-prepared; err != nil {
		t.Fatal(err)
	}
	assertParticipantStatus(t, facts, alias, participant.StatusPreparing)
	s.abortWork(alias)
	if len(facts) != 0 {
		t.Fatal("unsubscribed callback received queued or later facts")
	}
}

func TestSubscribeParticipants_shutdownReleasesSubscriptions(t *testing.T) {
	s, alias := newReadySubscriptionSession(t)
	facts := make(chan Event, 4)
	_, unsubscribe := s.SubscribeParticipants(func(event Event) { facts <- event })
	t.Cleanup(unsubscribe)
	s.Shutdown()
	_, stopLate := s.SubscribeParticipants(func(event Event) { facts <- event })
	t.Cleanup(stopLate)
	if err := s.prepareParticipantForWork(alias, 1); err != nil {
		t.Fatal(err)
	}
	if len(facts) != 0 {
		t.Fatal("shutdown retained subscriptions or accepted new callbacks")
	}
}

func TestSubscribeParticipants_readinessAndMembershipFacts(t *testing.T) {
	s := New()
	t.Cleanup(s.Shutdown)
	alias := registerIdleBeforeReadyParticipant(t, s)
	facts := make(chan Event, 8)
	initial, unsubscribe := s.SubscribeParticipants(func(event Event) { facts <- event })
	t.Cleanup(unsubscribe)
	if len(initial.Participants) != 1 || initial.Participants[0].StartupReady {
		t.Fatalf("initial startup snapshot = %#v", initial)
	}
	assertStartupReadyFact(t, s, alias, facts)
	if initial.Participants[0].StartupReady {
		t.Fatal("readiness change mutated the initial snapshot")
	}
	s.changeParticipants(func() { s.removeParticipantLocked(alias) })
	stopped, ok := receiveParticipantFact(t, facts).(AgentStopped)
	if !ok || stopped.Alias != alias {
		t.Fatal("membership removal did not retain the original identity")
	}
	replacement := participant.New(alias.Name, "", participant.InitiativeManual)
	replacement.BeginStartup(time.Now())
	if err := s.addParticipant(replacement); err != nil {
		t.Fatal(err)
	}
	assertParticipantStatus(t, facts, replacement.Alias, participant.StatusStarting)
	current, stopCurrent := s.SubscribeParticipants(func(Event) { t.Error("unexpected future fact") })
	t.Cleanup(stopCurrent)
	if len(current.Participants) != 1 || current.Participants[0].Alias != replacement.Alias || replacement.Alias == alias {
		t.Fatal("subscription snapshot mixed removed and replacement instances")
	}
}

func assertStartupReadyFact(t *testing.T, s *Session, alias participant.Alias, facts <-chan Event) {
	t.Helper()
	if err := s.updateParticipant(alias, func(p *participant.Participant) (Event, error) {
		return nil, p.SessionReady()
	}); err != nil {
		t.Fatal(err)
	}
	ready, ok := receiveParticipantFact(t, facts).(AgentReady)
	if !ok || ready.Alias != alias {
		t.Fatal("readiness change was missed")
	}
}

func TestSubscribeParticipants_statusCarriesActiveExecution(t *testing.T) {
	s, alias := newReadySubscriptionSession(t)
	facts := make(chan Event, 8)
	_, unsubscribe := s.SubscribeParticipants(func(event Event) { facts <- event })
	defer unsubscribe()
	if err := s.prepareParticipantForWork(alias, 1); err != nil {
		t.Fatal(err)
	}
	assertParticipantStatus(t, facts, alias, participant.StatusPreparing)
	s.beginParticipantWorking(alias, "anchor")
	working, ok := receiveParticipantFact(t, facts).(ParticipantStatusChanged)
	view, exists := s.ParticipantByName(alias.Name)
	if !ok || !exists || working.To != participant.StatusWorking || working.ActiveExecutionID == 0 || working.ActiveExecutionID != view.ActiveExecutionID {
		t.Fatalf("working fact=%#v, view=%#v", working, view)
	}
	s.abortWork(alias)
	idle, ok := receiveParticipantFact(t, facts).(ParticipantStatusChanged)
	if !ok || idle.To != participant.StatusIdle || idle.ActiveExecutionID != 0 {
		t.Fatalf("idle fact=%#v", idle)
	}
}

func startSubscribedWork(t *testing.T, s *Session, alias participant.Alias, id ExecutionID, facts <-chan Event) {
	t.Helper()
	if err := s.prepareParticipantForWork(alias, id); err != nil {
		t.Fatal(err)
	}
	assertParticipantStatus(t, facts, alias, participant.StatusPreparing)
	s.beginParticipantWorking(alias, "anchor")
	assertParticipantStatus(t, facts, alias, participant.StatusWorking)
}

func TestSubscribeParticipants_completionPrecedesIdleAndSurvivesLateSubscription(t *testing.T) {
	s, alias := newReadySubscriptionSession(t)
	facts := make(chan Event, 8)
	_, stop := s.SubscribeParticipants(func(event Event) { facts <- event })
	defer stop()
	startSubscribedWork(t, s, alias, 41, facts)
	s.handleAgentMessage(alias, agent.Message{StreamID: "anchor", Mode: agent.ModeFlush, Content: agent.Output{}})
	completed, ok := receiveParticipantFact(t, facts).(ParticipantWorkCompleted)
	if !ok || completed.Alias != alias || completed.ExecutionID != 41 {
		t.Fatalf("completion=%#v", completed)
	}
	assertParticipantStatus(t, facts, alias, participant.StatusIdle)
	snapshot := assertCompletionSnapshot(t, s, alias, 41)
	snapshot.Participants[0].LastCompletedExecutionID = 99 // Detached from Session state.
	startSubscribedWork(t, s, alias, 42, facts)
	s.abortWork(alias)
	assertParticipantStatus(t, facts, alias, participant.StatusIdle)
	assertCompletionSnapshot(t, s, alias, 41)
	if len(facts) != 0 {
		t.Fatal("abort emitted a completion")
	}
	s.changeParticipants(func() { s.removeParticipantLocked(alias) })
	after, unsubscribe := s.SubscribeParticipants(func(Event) {})
	defer unsubscribe()
	if len(after.Participants) != 0 {
		t.Fatal("departure retained completion history")
	}
}

func assertCompletionSnapshot(t *testing.T, s *Session, alias participant.Alias, expected ExecutionID) ParticipantSnapshot {
	t.Helper()
	initial, unsubscribe := s.SubscribeParticipants(func(Event) { t.Error("late subscriber replayed old events") })
	defer unsubscribe()
	if len(initial.Participants) != 1 || initial.Participants[0].Alias != alias || initial.Participants[0].ActiveExecutionID != 0 || initial.Participants[0].LastCompletedExecutionID != expected {
		t.Fatalf("snapshot=%#v", initial)
	}
	return initial
}
