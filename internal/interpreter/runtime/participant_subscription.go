package runtime

import (
	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/session"
)

// participantSubscription collects participant facts and requests advancement on
// the owner's lane. Only its event callback runs concurrently with lifecycle work.
type participantSubscription struct {
	updates        participantUpdates
	includes       func(participant.Alias) bool
	requestAdvance func()
	unsubscribe    func()
}

func (m *participantSubscription) initialize(ctx Context, includes func(participant.Alias) bool) {
	m.includes = includes
	m.updates.start()
	initial, unsubscribe := ctx.Session.SubscribeParticipants(m.handleEvent)
	m.unsubscribe = unsubscribe
	m.updates.merge(initial)
}

func (m *participantSubscription) handleEvent(event session.Event) {
	alias, ok := parseParticipantAlias(event)
	if !ok || !m.includes(alias) {
		return
	}
	if m.updates.collect(event) && m.requestAdvance != nil {
		m.requestAdvance()
	}
}

func (m *participantSubscription) readSnapshot() participantSnapshot {
	return m.updates.readSnapshot()
}

func (m *participantSubscription) commit(snapshot participantSnapshot) bool {
	return m.updates.commit(snapshot.revision)
}

func (m *participantSubscription) stop() {
	m.updates.stop()
	if m.unsubscribe != nil {
		m.unsubscribe()
		m.unsubscribe = nil
	}
}

func parseParticipantAlias(event session.Event) (participant.Alias, bool) {
	switch event := event.(type) {
	case session.ParticipantStatusChanged:
		return event.Alias, true
	case session.AgentReady:
		return event.Alias, true
	case session.AgentStopped:
		return event.Alias, true
	case session.AgentCrashed:
		return event.Alias, true
	default:
		return participant.Alias{}, false
	}
}
