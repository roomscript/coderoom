package ui

import "github.com/roomscript/coderoom/internal/interpreter"

type debugViewRequested struct{}
type debugRowsRequested struct{}

// ApplicationCommands supplies immutable, argument-free UI command registrations.
// Callbacks enqueue messages; only the UI event loop changes presentation state.
func (o *Observer) ApplicationCommands() []interpreter.ApplicationCommand {
	return []interpreter.ApplicationCommand{
		{Name: "debugview", Description: "print viewport debug", Run: func() error { return o.dispatch(debugViewRequested{}) }},
		{Name: "debugrows", Description: "toggle row number overlay", Run: func() error { return o.dispatch(debugRowsRequested{}) }},
	}
}

func (m Model) handleDebugView() Model {
	if !m.debug {
		return m.debugDisabled()
	}
	return m.debugView()
}
func (m Model) handleDebugRows() Model {
	if !m.debug {
		return m.debugDisabled()
	}
	m.room = m.room.ToggleDebugRowNums()
	return m
}
func (m Model) debugDisabled() Model {
	m.room = m.room.AppendSystem("error: debug commands disabled (set CODEROOM_DEBUG=1)")
	return m
}
