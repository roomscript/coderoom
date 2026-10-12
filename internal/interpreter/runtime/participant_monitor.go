package runtime

import (
	"sync"

	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/session"
)

// participantMonitor owns subscription, initial-state merging and the private
// wake-up queue. It delegates readiness policy to its owner's advancement callback.
// initialize/start/stop are owner-serialized; cancel and collection may race.
// stop is non-blocking so advancement can stop its own monitor; wait joins externally.
type participantMonitor struct {
	stopOnce    sync.Once
	cancelled   chan struct{}
	wake        chan struct{}
	done        chan struct{}
	updates     participantUpdates
	includes    func(participant.Alias) bool
	unsubscribe func()
}

func newParticipantMonitor() *participantMonitor {
	return &participantMonitor{
		cancelled: make(chan struct{}), wake: make(chan struct{}, 1),
	}
}

func (m *participantMonitor) initialize(ctx Context, includes func(participant.Alias) bool) {
	m.includes = includes
	m.updates.start()
	m.unsubscribe = ctx.Session.Subscribe(m.handleEvent)
	m.updates.merge(ctx.Participants.Participants())
}

func (m *participantMonitor) handleEvent(event session.Event) {
	alias, ok := parseParticipantAlias(event)
	if !ok || !m.includes(alias) {
		return
	}
	m.updates.collect(event)
	m.notifyStateChanged()
}

func (m *participantMonitor) notifyStateChanged() {
	// State is already stored. One pending wake-up is enough to check all changes;
	// skip redundant signals so the event producer never waits for the monitor.
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

func (m *participantMonitor) start(advance func() bool) {
	m.done = make(chan struct{})
	go m.run(advance)
}

func (m *participantMonitor) run(advance func() bool) {
	defer close(m.done)
	for {
		select {
		case <-m.cancelled:
			return
		case <-m.wake:
			if settled := advance(); settled {
				return
			}
		}
	}
}

func (m *participantMonitor) readSnapshot() participantSnapshot {
	return m.updates.readSnapshot()
}

func (m *participantMonitor) commit(snapshot participantSnapshot) bool {
	return m.updates.commit(snapshot.revision)
}

func (m *participantMonitor) cancel() {
	m.stopOnce.Do(func() { close(m.cancelled) })
}

func (m *participantMonitor) isStopped() bool {
	select {
	case <-m.cancelled:
		return true
	default:
		return false
	}
}

func (m *participantMonitor) stop() {
	m.cancel()
	m.updates.stop()
	if m.unsubscribe != nil {
		m.unsubscribe()
		m.unsubscribe = nil
	}
}

func (m *participantMonitor) wait() {
	if m.done != nil {
		<-m.done
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
