// Package approval implements the approval prompt input as a Bubble Tea component.
package approval

import (
	"github.com/roomscript/coderoom/internal/interpreter"
)

// Model holds the approval prompt and selection state.
type Model struct {
	alias            string
	participantColor string
	ask              string
	options          []interpreter.ApprovalOption
	selected         int
	width            int
	maxHeight        int
	requestTop       int
	fileChange       bool
	command          bool
	paths            []string
}

// New returns an empty Model.
func New() Model { return Model{} }

// Active reports whether an approval is currently being displayed.
func (m Model) Active() bool { return m.ask != "" && len(m.options) > 0 }

// Ask returns the prompt string.
func (m Model) Ask() string { return m.ask }

// Options returns the available options.
func (m Model) Options() []interpreter.ApprovalOption { return m.options }

// Selected returns the currently selected option index.
func (m Model) Selected() int { return m.selected }

// SelectedOption returns the currently selected option, if any.
func (m Model) SelectedOption() (interpreter.ApprovalChoice, bool) {
	if m.selected < 0 || m.selected >= len(m.options) {
		return interpreter.ApprovalChoice{}, false
	}
	return interpreter.ApprovalChoice{OptionID: m.options[m.selected].ID}, true
}

// Set sets the approval prompt and options and resets selection to 0.
func (m Model) Set(req interpreter.Approval) Model {
	m.alias = req.Alias.Name
	m.fileChange = req.Kind == interpreter.ApprovalFileChange
	m.command = req.Kind == interpreter.ApprovalCommandExecution
	m.paths = append([]string(nil), req.Paths...)
	m.ask = req.Prompt
	switch {
	case m.fileChange:
		m.ask = "File changes"
	case m.command:
		m.ask = req.Summary
		if m.ask == "" {
			m.ask = "shell command"
		}
		m.ask += "\nFull command in history"
	case req.Details != "":
		m.ask += "\n" + req.Details
	}
	m.options = append([]interpreter.ApprovalOption(nil), req.Options...)
	m.selected = 0
	m.requestTop = 0
	return m
}

// FileChange reports whether the compact file approval is displayed.
func (m Model) FileChange() bool { return m.fileChange }

// Compact reports whether the evidence is retained in history.
func (m Model) Compact() bool { return m.fileChange || m.command }

// SetParticipantColor uses the participant's assigned color for the header.
func (m Model) SetParticipantColor(color string) Model {
	m.participantColor = color
	return m
}

// Clear removes the current approval.
func (m Model) Clear() Model {
	m.alias = ""
	m.fileChange = false
	m.command = false
	m.paths = nil
	m.ask = ""
	m.options = nil
	m.selected = 0
	m.requestTop = 0
	return m
}

// SetSize bounds the approval panel within the room's available space.
func (m Model) SetSize(width, maxHeight int) Model {
	m.width = max(width, 1)
	m.maxHeight = max(maxHeight, 1)
	layout := m.layout()
	m.requestTop = min(m.requestTop, layout.maxRequestTop())
	return m
}

// Height reports the rendered panel height using the same layout as View.
func (m Model) Height() int {
	if !m.Active() {
		return 0
	}
	return m.layout().height()
}
