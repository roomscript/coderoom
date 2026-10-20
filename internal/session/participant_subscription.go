package session

import (
	"fmt"

	"github.com/roomscript/coderoom/internal/participant"
)

// ParticipantSnapshot contains detached views captured at one Session boundary.
type ParticipantSnapshot struct {
	Participants []participant.View
}

// changeParticipants owns the lifecycle mutation boundary. The callback describes
// lifecycle or membership changes together with Session runtime bookkeeping; it
// must not block, notify observers, or acquire the Session lock. Group captures
// changes under that lock and delivers them after unlocking, before the caller
// publishes ordinary observer events. Failed changes are compared to actual state.
func (s *Session) changeParticipants(change func()) {
	s.mu.Lock()
	before := s.participants.Snapshot()
	change()
	delivery := s.participants.CaptureChanges(before)
	s.mu.Unlock()
	delivery.Deliver()
}

// SubscribeParticipants atomically captures initial state and subscribes to later
// lifecycle facts in mutation order. Pre-snapshot facts are never replayed.
// Callbacks may race its return, but are never called inline by this method.
// They must only collect facts and return promptly. Unsubscribe drops queued facts
// without waiting for an in-flight callback and is safe from a callback.
func (s *Session) SubscribeParticipants(handle func(Event)) (ParticipantSnapshot, func()) {
	s.mu.Lock()
	initial, subscription := s.participants.Subscribe(func(change participant.Change) {
		handle(deriveParticipantEvent(change))
	})
	s.mu.Unlock()
	return ParticipantSnapshot{Participants: initial}, func() {
		s.mu.Lock()
		s.participants.Unsubscribe(subscription)
		s.mu.Unlock()
	}
}

func (s *Session) closeParticipantSubscriptions() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.participants.CloseSubscriptions()
}

// Group reports participant facts; Session defines their runtime event vocabulary.
func deriveParticipantEvent(change participant.Change) Event {
	before, after := change.Before, change.After
	switch change.Kind {
	case participant.StatusChanged:
		return ParticipantStatusChanged{Alias: after.Alias, From: before.Status, To: after.Status, Since: after.Since, ActiveExecutionID: after.ActiveExecutionID}
	case participant.BecameReady:
		return AgentReady{Alias: after.Alias, Settings: after.Settings}
	case participant.WorkCompleted:
		return ParticipantWorkCompleted{Alias: before.Alias, ExecutionID: after.LastCompletedExecutionID}
	case participant.Removed:
		return AgentStopped{Alias: before.Alias}
	default:
		panic(fmt.Sprintf("unknown participant change kind %d", change.Kind))
	}
}
