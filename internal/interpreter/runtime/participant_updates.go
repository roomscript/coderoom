package runtime

import (
	"maps"
	"sync"

	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/session"
)

// participantUpdates collects facts immediately, independently of serialized
// lifecycle evaluation. Each state belongs to one resolved participant instance.
type participantUpdates struct {
	mu       sync.Mutex
	active   bool
	states   map[participant.Alias]participantState
	revision uint64
}

type participantSnapshot struct {
	states   map[participant.Alias]participantState
	revision uint64
}

type participantState struct {
	status                   participant.Status
	lastCompletedExecutionID participant.ExecutionID
	startupReady             bool
	departed                 bool
}

func (s participantState) isReadyForWork() bool {
	view := participant.View{Status: s.status, StartupReady: s.startupReady}
	return !s.departed && view.IsReadyForWork()
}

func (u *participantUpdates) start() {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.active = true
	if u.states == nil {
		u.states = make(map[participant.Alias]participantState)
	}
}

func (u *participantUpdates) stop() {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.active = false
}

func (u *participantUpdates) collect(event session.Event) bool {
	alias, ok := parseParticipantAlias(event)
	if !ok {
		return false
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if !u.active {
		return false
	}
	state := u.states[alias]
	if state.departed {
		return false
	}
	applyParticipantUpdate(&state, event)
	u.states[alias] = state
	u.revision++
	return true
}

func applyParticipantUpdate(state *participantState, event session.Event) {
	switch event := event.(type) {
	case session.ParticipantWorkCompleted:
		state.lastCompletedExecutionID = max(state.lastCompletedExecutionID, event.ExecutionID)
	case session.ParticipantStatusChanged:
		state.applyStatus(event)
	case session.AgentReady:
		state.status, state.startupReady = participant.StatusIdle, true
	case session.AgentStopped, session.AgentCrashed:
		state.departed, state.startupReady = true, false
	}
}

func (s *participantState) applyStatus(event session.ParticipantStatusChanged) {
	s.status = event.To
	s.departed = event.To == participant.StatusCrashed
	switch {
	case event.To == participant.StatusStarting || event.To == participant.StatusAttached ||
		event.From == participant.StatusStarting || event.From == participant.StatusAttached:
		s.startupReady = false
	case isPastStartup(event.From) || isPastStartup(event.To):
		s.startupReady = true
	}
}

func isPastStartup(status participant.Status) bool {
	switch status {
	case participant.StatusPreparing, participant.StatusWorking, participant.StatusKeepalive:
		return true
	default:
		return false
	}
}

// merge preserves newer event state while merging completion history independently.
func (u *participantUpdates) merge(initial session.ParticipantSnapshot) {
	u.mu.Lock()
	defer u.mu.Unlock()
	for _, view := range initial.Participants {
		if _, exists := u.states[view.Alias]; !exists {
			u.states[view.Alias] = participantState{
				status: view.Status, startupReady: view.StartupReady,
				departed: view.Status == participant.StatusCrashed,
			}
			u.revision++
		}
		state := u.states[view.Alias]
		state.lastCompletedExecutionID = max(state.lastCompletedExecutionID, view.LastCompletedExecutionID)
		u.states[view.Alias] = state
		u.revision++
	}
}

func (u *participantUpdates) readSnapshot() participantSnapshot {
	u.mu.Lock()
	defer u.mu.Unlock()
	return participantSnapshot{states: maps.Clone(u.states), revision: u.revision}
}

// commit freezes collection only if the evaluated facts are still current.
// No command callback or execution runs under the collector lock.
func (u *participantUpdates) commit(revision uint64) bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	if !u.active || revision != u.revision {
		return false
	}
	u.active = false
	return true
}
