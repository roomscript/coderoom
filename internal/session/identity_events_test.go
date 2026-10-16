package session_test

import (
	"sync"
	"testing"
	"time"

	"github.com/roomscript/coderoom/internal/agent"
	roomconfig "github.com/roomscript/coderoom/internal/config"
	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/session"
)

type blockedDepartureObserver struct {
	alias   participant.Alias
	entered chan struct{}
	release chan struct{}
}

func (o *blockedDepartureObserver) OnEvent(event session.Event) {
	if stopped, ok := event.(session.AgentStopped); ok && stopped.Alias == o.alias {
		close(o.entered)
		<-o.release
	}
}

func TestParticipantSend_delayedDepartureCannotTransferToReplacement(t *testing.T) {
	observer := newTestObserver()
	blocker := &blockedDepartureObserver{entered: make(chan struct{}), release: make(chan struct{})}
	original, replacement := newMockAgent(), newMockAgent()
	current := original
	s := newSession(t, session.WithObserver(blocker), session.WithObserver(observer), session.WithAgentFactory(func(_ *session.Session, _ roomconfig.ParticipantConfig, _ session.AgentBackend) agent.Agent {
		return current
	}))
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(blocker.release) }) }
	t.Cleanup(release)
	invite(t, s, "ada")
	old := mustReceive[session.AgentReady](t, observer.ch).Alias
	blocker.alias = old
	plan := s.CreateParticipantSendPlan(old)
	removeWithBlockedDeparture(t, s, old, blocker)
	current = replacement
	invite(t, s, "ada")
	ready := mustReceive[session.AgentReady](t, observer.ch)
	if ready.Alias.ID == old.ID {
		t.Fatal("replacement reused identity")
	}
	if _, err := s.Execute(session.SendToParticipantCommand{Plan: plan, Message: "retained work"}); err == nil {
		t.Fatal("retained send accepted replacement before old departure arrived")
	}
	assertNoIdentityTestDelivery(t, []*mockAgent{original, replacement})
	outcome := mustReceive[session.RoutingCompleted](t, observer.ch)
	if len(outcome.Result.Recipients) != 1 || outcome.Result.Recipients[0].Alias != old || outcome.Result.Recipients[0].Status != session.DeliveryFailed {
		t.Fatalf("retained send outcome = %#v", outcome)
	}
	release()
	stopped := mustReceive[session.AgentStopped](t, observer.ch)
	if stopped.Alias != old {
		t.Fatalf("delayed departure alias = %+v, want %+v", stopped.Alias, old)
	}
	if view, ok := s.Participant(ready.Alias); !ok || !view.IsReadyForWork() {
		t.Fatal("delayed departure changed replacement membership or readiness")
	}
}

func removeWithBlockedDeparture(t *testing.T, s *session.Session, old participant.Alias, blocker *blockedDepartureObserver) {
	t.Helper()
	if _, err := s.Execute(session.RemoveCommand{Alias: old}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-blocker.entered:
	case <-time.After(time.Second):
		t.Fatal("old departure did not reach blocking observer")
	}
}
