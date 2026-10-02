// Package approval implements the approval prompt input as a Bubble Tea component.
package approval

import (
	"github.com/trigosec/coderoom/internal/interpreter"
)

// Model holds the approval prompt and selection state.
type Model struct {
	ask      string
	options  []interpreter.ApprovalOption
	selected int
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
	m.ask = req.Prompt
	m.options = append([]interpreter.ApprovalOption(nil), req.Options...)
	m.selected = 0
	return m
}

// Clear removes the current approval.
func (m Model) Clear() Model {
	m.ask = ""
	m.options = nil
	m.selected = 0
	return m
}
