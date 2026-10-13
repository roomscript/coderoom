package session_test

import (
	"sync"
	"testing"
	"time"

	"github.com/roomscript/coderoom/internal/agent"
	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/session"
)

type blockedReadyObserver struct {
	entered chan participant.Alias
	release chan struct{}
}

func (o *blockedReadyObserver) OnEvent(event session.Event) {
	if ready, ok := event.(session.AgentReady); ok {
		o.entered <- ready.Alias
		<-o.release
	}
}

// A locked Session snapshot is not an event-collection barrier. Hosts combining
// lifecycle subscriptions with initial reads must supply that ordering themselves.
func TestParticipants_canOvertakeReadyNotification(t *testing.T) {
	blocker := &blockedReadyObserver{entered: make(chan participant.Alias, 1), release: make(chan struct{})}
	observer := newTestObserver()
	s := newSession(t, session.WithObserver(blocker), session.WithObserver(observer), mappedFactory(map[string]agent.Agent{"ada": newMockAgent()}))
	var once sync.Once
	release := func() { once.Do(func() { close(blocker.release) }) }
	t.Cleanup(release)
	invite(t, s, "ada")
	var alias participant.Alias
	select {
	case alias = <-blocker.entered:
	case <-time.After(time.Second):
		t.Fatal("ready notification did not reach blocking observer")
	}
	views := s.Participants()
	if len(views) != 1 || views[0].Alias != alias || !views[0].IsReadyForWork() {
		t.Fatalf("snapshot did not expose committed readiness: %#v", views)
	}
	observer.mu.Lock()
	for _, event := range observer.events {
		if _, ok := event.(session.AgentReady); ok {
			observer.mu.Unlock()
			t.Fatal("ready notification passed the blocked observer")
		}
	}
	observer.mu.Unlock()
	release()
	if ready := mustReceive[session.AgentReady](t, observer.ch); ready.Alias != alias {
		t.Fatalf("delivered readiness belongs to %v, want %v", ready.Alias, alias)
	}
}
