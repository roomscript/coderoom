package compose

// cursorVisualRowCol returns the cursor position in visual (soft-wrapped)
// coordinates relative to the top of the full rendered buffer (not the
// viewport). Column is relative to the start of the content area.
func (m Model) cursorVisualRowCol() (row int, col int) {
	li := m.input.LineInfo()
	return m.wrapped.offsets[m.input.Line()] + li.RowOffset, li.ColumnOffset
}
