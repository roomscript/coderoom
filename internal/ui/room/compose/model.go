// Package compose implements the text-input area as a Bubble Tea component.
package compose

import (
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
)

// Model holds the textarea and its height constraints.
type Model struct {
	input     textarea.Model
	maxH      int
	visH      int // total visual rows (cached)
	scrollOff int // first visible visual row (cached)
	wrapped   wrapCache
}

// New creates a focused Model with default decorations applied.
func New() Model {
	ti := textarea.New()
	ti.ShowLineNumbers = false
	ti.KeyMap.WordForward = key.NewBinding(key.WithKeys("alt+right", "alt+f", "ctrl+right"))
	ti.KeyMap.WordBackward = key.NewBinding(key.WithKeys("alt+left", "alt+b", "ctrl+left"))
	ti = applyDecorations(ti)
	ti.Focus()
	return (Model{input: ti}).recalcHeight()
}

// Init returns the textarea cursor-blink command.
func (m Model) Init() tea.Cmd { return textarea.Blink }

// SetWidth sets the display width.
func (m Model) SetWidth(w int) Model {
	m.input.SetWidth(w)
	return m.recalcHeight()
}

// SetMaxHeightFromTotal derives the input height cap from the total terminal
// height and recalculates the current input height.
func (m Model) SetMaxHeightFromTotal(totalH int) Model {
	// Keep space for:
	//   - at least 1 line of history viewport
	//   - 2 separator lines (top + bottom)
	//
	// When the terminal is very small, this clamps the input to avoid the layout
	// exceeding totalH.
	m.maxH = min(inputMaxHeight(totalH), max(totalH-3, 1))
	return m.recalcHeight()
}

// Height returns the current rendered height of the input area.
func (m Model) Height() int { return m.input.Height() }

// Value returns the current text content.
func (m Model) Value() string { return m.input.Value() }

// SetValue replaces the text content and recalculates height.
func (m Model) SetValue(s string) Model {
	m.input.SetValue(s)
	return m.recalcHeight()
}

// Reset clears the text content and recalculates height.
func (m Model) Reset() Model {
	m.input.Reset()
	return m.recalcHeight()
}

// Focus focuses the textarea cursor.
func (m Model) Focus() (Model, tea.Cmd) {
	cmd := m.input.Focus()
	return m, cmd
}

// Blur removes focus from the textarea.
func (m Model) Blur() Model {
	m.input.Blur()
	return m
}

func inputMaxHeight(totalH int) int {
	// Prefer enough room to show a short wrapped paragraph without immediately
	// hiding the first visual row.
	return min(8, max(totalH/3, 3))
}

// HasAbove reports whether content is scrolled above the visible area.
func (m Model) HasAbove() bool { return m.scrollOff > 0 }

// HasBelow reports whether content extends below the visible area.
func (m Model) HasBelow() bool { return m.scrollOff+m.input.Height() < m.visH }

func (m Model) recalcHeight() Model {
	m.wrapped.update(m.input.Value(), m.input.Width())
	if m.maxH > 0 {
		// textarea.Model.Width() is the *content width* (prompt/borders excluded),
		// so do not subtract promptWidth here.
		m.visH = m.wrapped.visualRows()
		h := min(max(m.visH, 1), m.maxH)
		if m.input.Height() != h {
			m.input.SetHeight(h)
		}
		m.scrollOff = m.computeScrollOff()
	}
	return m
}

// computeScrollOff keeps the cursor within the visible region, matching the
// textarea viewport behavior but using our own rendered-line model.
func (m Model) computeScrollOff() int {
	h := m.input.Height()
	if h <= 0 || m.visH <= h {
		return 0
	}
	cursorRow := m.cursorVisualRow()
	if cursorRow < 0 {
		cursorRow = 0
	}
	maxOff := max(0, m.visH-h)

	off := m.scrollOff
	if off < 0 {
		off = 0
	}
	if off > maxOff {
		off = maxOff
	}

	if cursorRow < off {
		off = cursorRow
	} else if cursorRow >= off+h {
		off = cursorRow - h + 1
	}
	return clamp(off, 0, maxOff)
}

func (m Model) cursorVisualRow() int {
	row, _ := m.cursorVisualRowCol()
	return row
}

func clamp(v, low, high int) int {
	if high < low {
		panic("clamp: high < low")
	}
	return min(high, max(low, v))
}
