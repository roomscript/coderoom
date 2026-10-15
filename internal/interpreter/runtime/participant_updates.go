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
	status       participant.Status
	startupReady bool
	departed     bool
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
	case session.ParticipantStatusChanged:
		state.status = event.To
		state.departed = event.To == participant.StatusCrashed
		switch {
		case event.To == participant.StatusStarting || event.To == participant.StatusAttached ||
			event.From == participant.StatusStarting || event.From == participant.StatusAttached:
			state.startupReady = false
		case isPastStartup(event.From) || isPastStartup(event.To):
			state.startupReady = true
		}
	case session.AgentReady:
		state.status, state.startupReady = participant.StatusIdle, true
	case session.AgentStopped, session.AgentCrashed:
		state.departed, state.startupReady = true, false
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

// merge only fills aliases untouched by events, including any departures.
func (u *participantUpdates) merge(views []participant.View) {
	u.mu.Lock()
	defer u.mu.Unlock()
	for _, view := range views {
		if _, exists := u.states[view.Alias]; !exists {
			u.states[view.Alias] = participantState{
				status: view.Status, startupReady: view.StartupReady,
				departed: view.Status == participant.StatusCrashed,
			}
			u.revision++
		}
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
