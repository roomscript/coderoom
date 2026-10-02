// Package staging implements the "barrier-batch" staging model for shared sends:
// user-authored sends/broadcasts are staged until all targeted participants are idle.
package staging

import (
	"slices"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/trigosec/coderoom/internal/participant"
	"github.com/trigosec/coderoom/internal/session"
)

// ActionKind describes the staged action type.
type ActionKind uint8

const (
	// ActionUnknown is the zero value; it indicates an invalid/unset action.
	ActionUnknown ActionKind = iota
	// ActionSend represents a direct send to a single alias.
	ActionSend
	// ActionBroadcast represents a broadcast send to all routed participants.
	ActionBroadcast
)

// Action is the normalized staged action payload.
type Action struct {
	Kind     ActionKind
	Alias    string // for ActionSend
	SendPlan session.SharedSendPlan
	Text     string
}

// Batch represents a staged submission waiting on a barrier of participants.
type Batch struct {
	Raw       string
	Action    Action
	Barrier   []string
	Discarded map[string]bool
	Interrupt bool
}

// NewBatch constructs a staged batch and copies/sorts the barrier slice.
func NewBatch(raw string, action Action, barrier []string) *Batch {
	cp := make([]string, len(barrier))
	copy(cp, barrier)
	slices.Sort(cp)
	return &Batch{
		Raw:       raw,
		Action:    action,
		Barrier:   cp,
		Discarded: make(map[string]bool),
	}
}

// MarkDiscarded removes the alias from future targeting (idempotent).
func (b *Batch) MarkDiscarded(alias string) {
	if b == nil {
		return
	}
	b.Discarded[alias] = true
}

// ActiveTargets returns the remaining non-discarded aliases for this batch.
func (b *Batch) ActiveTargets() []string {
	if b == nil {
		return nil
	}
	out := make([]string, 0, len(b.Barrier))
	for _, alias := range b.Barrier {
		if b.Discarded[alias] {
			continue
		}
		out = append(out, alias)
	}
	return out
}

// BlockedTargets returns the active targets that are not idle. Missing
// participants are discarded and excluded from the returned list.
func (b *Batch) BlockedTargets(statusByAlias func(alias string) (participant.Status, bool)) []string {
	if b == nil {
		return nil
	}
	var blocked []string
	for _, alias := range b.ActiveTargets() {
		st, ok := statusByAlias(alias)
		if !ok {
			b.MarkDiscarded(alias)
			continue
		}
		if st != participant.StatusIdle {
			blocked = append(blocked, alias)
		}
	}
	slices.Sort(blocked)
	return blocked
}

// ColorByAlias returns a lipgloss-compatible color string for the given alias.
type ColorByAlias func(alias string) string

// RenderStatusLine renders the staged status line shown while a batch is held.
func RenderStatusLine(staged *Batch, blocked []string, colorByAlias ColorByAlias) string {
	var parts []string
	if staged != nil && staged.Interrupt {
		parts = append(parts, "Interrupt requested.")
	} else {
		parts = append(parts, "Message on-hold.")
	}
	if len(blocked) > 0 {
		parts = append(parts, "Participants busy: "+renderAliasList(blocked, colorByAlias)+".")
	} else {
		parts = append(parts, "Participants busy: none.")
	}
	if staged != nil && staged.Interrupt {
		parts = append(parts, "Waiting to send…")
	} else {
		parts = append(parts, "Press Esc to edit. Press Ctrl+X to interrupt and send.")
	}
	return strings.Join(parts, " ")
}

func renderAliasList(aliases []string, colorByAlias ColorByAlias) string {
	colored := make([]string, len(aliases))
	for i, alias := range aliases {
		color := ""
		if colorByAlias != nil {
			color = colorByAlias(alias)
		}
		if color == "" {
			colored[i] = alias
			continue
		}
		colored[i] = lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Render(alias)
	}
	return strings.Join(colored, ", ")
}
