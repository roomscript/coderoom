package session

import (
	"sync"
	"testing"
	"time"

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
	go func() { prepared <- s.prepareParticipantForWork(alias) }()
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
	if len(initial) != 1 || !initial[0].IsReadyForWork() {
		t.Fatalf("initial snapshot = %#v", initial)
	}
	prepared := startBlockedPreparation(t, s, alias, entered)
	s.beginParticipantWorking(alias, "anchor")
	laterFacts := make(chan Event, 4)
	later, stopLater := s.SubscribeParticipants(func(event Event) { laterFacts <- event })
	t.Cleanup(stopLater)
	if len(later) != 1 || later[0].Status != participant.StatusWorking || !initial[0].IsReadyForWork() {
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
	if err := s.prepareParticipantForWork(alias); err != nil {
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
	if len(initial) != 1 || initial[0].StartupReady {
		t.Fatalf("initial startup snapshot = %#v", initial)
	}
	assertStartupReadyFact(t, s, alias, facts)
	if initial[0].StartupReady {
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
	if len(current) != 1 || current[0].Alias != replacement.Alias || replacement.Alias == alias {
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
