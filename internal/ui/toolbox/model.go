// Package toolbox implements the participant status bar as a Bubble Tea component.
package toolbox

import (
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/roomscript/coderoom/internal/participant"
)

// Model is the state for the participant status bar.
type Model struct {
	width        int
	participants []participant.View
	now          func() time.Time
	tickActive   bool
}

// New creates a Model with time.Now as the clock.
func New() Model {
	return Model{now: time.Now}
}

// SetWidth sets the display width (inner, without margins).
func (m Model) SetWidth(w int) Model {
	m.width = w
	return m
}

// SetParticipants updates the participant snapshot and ensures the animation
// tick is running if any participant is in an active state.
func (m Model) SetParticipants(ps []participant.View) (Model, tea.Cmd) {
	m.participants = ps
	return m.ensureTick()
}

// Height returns the fixed number of lines rendered by View.
func (m Model) Height() int { return 1 }

// WantsTick reports whether any participant is in an active state
// that requires periodic redraw.
func (m Model) WantsTick() bool {
	for _, p := range m.participants {
		switch p.Status {
		case participant.StatusStarting, participant.StatusAttached, participant.StatusPreparing, participant.StatusKeepalive, participant.StatusWorking, participant.StatusCrashed:
			return true
		case participant.StatusIdle:
			// no tick needed
		}
	}
	return false
}
