package compose

import (
	tea "charm.land/bubbletea/v2"
)

// Update handles messages. Ctrl+C clears the input; Alt+Enter inserts a
// newline; all other keys are delegated to the textarea.
// Enter without Alt and Ctrl+G are intentionally not handled here — they
// carry ui-level semantics (submit, open editor) and are intercepted by the
// parent before reaching this Update.
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		return m.handleKey(msg)
	default:
		// Paste events and other textarea-driven messages arrive here.
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m.recalcHeight(), cmd
	}
}

func (m Model) handleKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	if out, ok := m.handleCtrlCKey(msg); ok {
		return out, nil
	}
	if out, ok := m.handleEnterKey(msg); ok {
		return out, nil
	}
	if out, cmd, ok := m.handleNavKey(msg); ok {
		return out, cmd
	}
	return m.handleTextKey(msg)
}

func (m Model) handleCtrlCKey(msg tea.KeyPressMsg) (Model, bool) {
	if !isCtrlKey(msg, 'c') {
		return m, false
	}
	if m.input.Value() == "" {
		return m, true
	}
	m.input.Reset()
	return m.recalcHeight(), true
}

func (m Model) handleEnterKey(msg tea.KeyPressMsg) (Model, bool) {
	k := msg.Key()
	if k.Code != tea.KeyEnter {
		return m, false
	}
	if k.Mod.Contains(tea.ModAlt) {
		m.input.InsertRune('\n')
		return m.recalcHeight(), true
	}
	// Non-Alt Enter is handled by the parent; treat as no-op here.
	return m, true
}

func (m Model) handleNavKey(msg tea.KeyPressMsg) (Model, tea.Cmd, bool) {
	k := msg.Key()
	switch k.Code {
	case tea.KeyUp:
		// When already on the first visual row of the buffer, Up moves to the
		// first character.
		li := m.input.LineInfo()
		if m.input.Line() == 0 && li.RowOffset == 0 && li.ColumnOffset > 0 {
			m.input.CursorStart()
			return m.recalcHeight(), nil, true
		}
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m.recalcHeight(), cmd, true
	case tea.KeyDown:
		// When already on the last line, Down moves to the last character.
		lastLine := m.input.LineCount() - 1
		li := m.input.LineInfo()
		if m.input.Line() >= lastLine && li.RowOffset+1 >= li.Height {
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnd}))
			return m.recalcHeight(), cmd, true
		}
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m.recalcHeight(), cmd, true
	default:
		return m, nil, false
	}
}

func (m Model) handleTextKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m.recalcHeight(), cmd
}

func isCtrlKey(msg tea.KeyPressMsg, key rune) bool {
	if msg.String() == "ctrl+"+string(key) {
		return true
	}
	k := msg.Key()
	return k.Code == key && k.Mod.Contains(tea.ModCtrl)
}
