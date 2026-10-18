package monolith

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/promptlang"
	"github.com/roomscript/coderoom/internal/session"
)

type submitContractSession struct {
	mu                    sync.Mutex
	observer              session.Observer
	participants          []participant.View
	readinessRequirements []participant.Participant
	executed              chan session.Command
	executeErr            error
	execute               func(session.Command, session.Observer)
	routingOverride       bool
	active                atomic.Int32
	maxActive             atomic.Int32
	shutdowns             atomic.Int32
}

func newSubmitContractSession() *submitContractSession {
	return &submitContractSession{executed: make(chan session.Command, 32)}
}

func (s *submitContractSession) Execute(command session.Command) (session.ExecutionID, error) {
	active := s.active.Add(1)
	defer s.active.Add(-1)
	for {
		maximum := s.maxActive.Load()
		if active <= maximum || s.maxActive.CompareAndSwap(maximum, active) {
			break
		}
	}
	s.mu.Lock()
	observer := s.observer
	execute := s.execute
	err := s.executeErr
	s.mu.Unlock()
	if execute != nil {
		execute(command, observer)
	}
	if observer != nil && !s.routingOverride {
		emitFakeRoutingOutcome(command, err, observer)
	}
	s.executed <- command
	return 1, err
}

func (s *submitContractSession) AddObserver(observer session.Observer) session.ParticipantSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.observer = observer
	return session.ParticipantSnapshot{}
}

func (*submitContractSession) CreateParticipantSendPlan(alias participant.Alias) session.ParticipantSendPlan {
	planner := session.New()
	defer planner.Shutdown()
	return planner.CreateParticipantSendPlan(alias)
}

func (s *submitContractSession) Participants() []participant.View {
	s.mu.Lock()
	defer s.mu.Unlock()
	values := append([]participant.View(nil), s.participants...)
	for _, p := range s.readinessRequirements {
		found := false
		for i := range values {
			if values[i].Alias == p.Alias {
				values[i] = p.View
				found = true
				break
			}
		}
		if !found {
			values = append(values, p.View)
		}
	}
	return values
}

func (s *submitContractSession) Participant(alias participant.Alias) (participant.View, bool) {
	for _, p := range s.Participants() {
		if p.Alias == alias {
			return p, true
		}
	}
	return participant.View{}, false
}

func (s *submitContractSession) ParticipantByName(name string) (participant.View, bool) {
	for _, value := range s.Participants() {
		if value.Alias.Name == name {
			return value, true
		}
	}
	return participant.View{Alias: testAlias(name)}, true
}

func (s *submitContractSession) Shutdown() { s.shutdowns.Add(1) }

type submitContractObserver struct{ events chan Event }

// Semantic contract tests consume the semantic stream; transcript transport has dedicated coverage.
func (o submitContractObserver) OnEvent(event Event) {
	if _, transcript := event.(TranscriptChanged); !transcript {
		o.events <- event
	}
}

func newSubmitContractInterpreter(t *testing.T) (*Interpreter, *submitContractSession, chan Event) {
	t.Helper()
	sess := newSubmitContractSession()
	events := make(chan Event, 64)
	interp := New(context.Background(), sess, t.TempDir(), WithObserver(submitContractObserver{events: events}))
	receiveSubmitEvent[StateChanged](t, events)
	t.Cleanup(interp.Close)
	return interp, sess, events
}

func mustSubmit(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
}

func receiveSubmitEvent[T Event](t *testing.T, events <-chan Event) T {
	t.Helper()
	select {
	case event := <-events:
		if _, transcript := event.(TranscriptChanged); transcript {
			return receiveSubmitEvent[T](t, events)
		}
		value, ok := event.(T)
		if !ok {
			t.Fatalf("event type = %T, want %T", event, *new(T))
		}
		return value
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %T", *new(T))
		var zero T
		return zero
	}
}

func receiveSubmitCommand(t *testing.T, commands <-chan session.Command) session.Command {
	t.Helper()
	select {
	case command := <-commands:
		return command
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for session command")
		return nil
	}
}

func assertNoSubmitEvent(t *testing.T, events <-chan Event) {
	t.Helper()
	select {
	case event := <-events:
		t.Fatalf("unexpected event: %#v", event)
	case <-time.After(20 * time.Millisecond):
	}
}

func receiveSignal(t *testing.T, signals <-chan struct{}, description string) {
	t.Helper()
	select {
	case <-signals:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %s", description)
	}
}

func assertNoSignal(t *testing.T, signals <-chan struct{}, description string) {
	t.Helper()
	select {
	case <-signals:
		t.Fatal(description)
	case <-time.After(20 * time.Millisecond):
	}
}

func emitFakeRoutingOutcome(command session.Command, err error, observer session.Observer) {
	result := session.RoutingResult{Err: err}
	var aliases []participant.Alias
	role := session.RecipientPrimary
	switch command := command.(type) {
	case session.BroadcastCommand:
		result.Kind, aliases, role = session.RoutingBroadcast, command.Aliases, session.RecipientBroadcast
	case session.SendToParticipantCommand:
		result.Kind, aliases = session.RoutingParticipantSend, command.Plan.Targets()
	case session.HandoffCommand:
		result.Kind, aliases, role = session.RoutingHandoff, []participant.Alias{command.ToAlias}, session.RecipientHandoff
	default:
		return
	}
	for _, alias := range aliases {
		status := session.DeliveryDelivered
		if err != nil {
			status = session.DeliveryFailed
		}
		result.Recipients = append(result.Recipients, session.RecipientResult{Alias: alias, Role: role, Status: status, Err: err})
	}
	observer.OnEvent(session.RoutingCompleted{Result: result})
}

func parseSourceStatement(t *testing.T, raw string) promptlang.ParsedStatement {
	t.Helper()
	parsed, err := promptlang.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}
