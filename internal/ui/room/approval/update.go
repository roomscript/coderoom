package approval

import (
	tea "charm.land/bubbletea/v2"
)

// ConfirmMsg is emitted when the user confirms the currently selected option.
type ConfirmMsg struct{}

// CancelMsg is emitted when the user cancels/dismisses the approval prompt.
type CancelMsg struct{}

// Update handles key navigation for approvals.
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	key, ok := msg.(tea.KeyPressMsg)
	if !ok || !m.Active() {
		return m, nil
	}
	return m.handleKey(key)
}

func (m Model) handleKey(key tea.KeyPressMsg) (Model, tea.Cmd) {
	k := key.Key()
	switch k.Code {
	case tea.KeyUp, 'k':
		return m.move(-1), nil
	case tea.KeyDown, 'j':
		return m.move(1), nil
	case tea.KeyPgUp:
		return m.scrollRequest(-1), nil
	case tea.KeyPgDown:
		return m.scrollRequest(1), nil
	case tea.KeyEnter:
		return m, func() tea.Msg { return ConfirmMsg{} }
	case tea.KeyEsc:
		return m, func() tea.Msg { return CancelMsg{} }
	default:
		return m, nil
	}
}

func (m Model) move(delta int) Model {
	n := len(m.options)
	if n == 0 {
		return m
	}
	m.selected = (m.selected + delta) % n
	if m.selected < 0 {
		m.selected += n
	}
	return m
}

func (m Model) scrollRequest(direction int) Model {
	layout := m.layout()
	if layout.requestRows == 0 {
		return m
	}
	step := layout.requestRows
	m.requestTop = min(max(m.requestTop+direction*step, 0), layout.maxRequestTop())
	return m
}
