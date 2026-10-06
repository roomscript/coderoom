package history

import (
	"strings"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	rec "github.com/roomscript/coderoom/internal/ui/room/history/record"
)

// SetSize initialises or resizes the viewport.
func (m Model) SetSize(w, h int) Model {
	prevWidth := 0
	if !m.viewportReady {
		m.viewport = viewport.New(viewport.WithWidth(w), viewport.WithHeight(h))
		m.viewportReady = true
	} else {
		prevWidth = m.viewport.Width()
		m.viewport.SetWidth(w)
		m.viewport.SetHeight(h)
	}
	return m.syncViewport(prevWidth != w)
}

// SetHeight adjusts the viewport height and re-syncs content.
func (m Model) SetHeight(h int) Model {
	m.viewport.SetHeight(h)
	return m.syncViewport(false)
}

// RebuildColors re-renders every record using the current color resolution.
func (m Model) RebuildColors() Model {
	m.colorVersion++
	return m.syncViewport(false)
}

// IsReasoningStreaming reports whether alias has an open reasoning stream.
func (m Model) IsReasoningStreaming(alias string) bool {
	for _, slot := range m.streaming {
		r := m.records[slot.recordIdx].record
		if r.Alias != alias || r.Msg == nil {
			continue
		}
		if r.Kind == rec.KindReasoning {
			return true
		}
	}
	return false
}

// Update forwards the message to the viewport (handles mouse scroll, etc.).
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	return m, cmd
}

// CursorVisible reports whether the history cursor has been initialised.
func (m Model) CursorVisible() bool { return m.cursor.Visible }

// CursorPosition reports the current cursor row and column on the visible surface.
func (m Model) CursorPosition() (int, int) { return m.cursor.Row, m.cursor.Col }

// HasSelection reports whether a visible selection range is active.
func (m Model) HasSelection() bool { return m.selection.Visible }

// CursorAtLiveEnd reports whether the history cursor is at the live end.
func (m Model) CursorAtLiveEnd() bool { return m.cursorAtLiveEnd() }

// RevealCursor scrolls just enough to bring the current history cursor into view.
func (m Model) RevealCursor() Model { return m.ensureCursorVisible() }

// AdoptCursorFromViewport places the history cursor onto the currently visible
// viewport without moving that viewport.
func (m Model) AdoptCursorFromViewport() Model {
	if len(m.lines) == 0 {
		m.cursor = Cursor{}
		return m
	}
	top := clampInt(m.viewport.YOffset(), len(m.lines)-1)
	height := max(m.viewport.Height(), 1)
	row := min(top+height-1, len(m.lines)-1)
	m.cursor = Cursor{
		Row:          row,
		Col:          0,
		PreferredCol: 0,
		Visible:      true,
	}
	return m
}

// ShowCursorInViewport keeps the current viewport position and, if needed,
// moves the history cursor onto the visible surface.
func (m Model) ShowCursorInViewport() Model {
	if !m.hasCursor() {
		return m
	}
	top := m.viewport.YOffset()
	height := max(m.viewport.Height(), 1)
	bottom := min(top+height-1, len(m.lines)-1)
	if m.cursor.Row < top {
		m.cursor.Row = top
	} else if m.cursor.Row > bottom {
		m.cursor.Row = bottom
	}
	lineEnd := lineWidth(m.lines[m.cursor.Row])
	if m.cursor.PreferredCol > lineEnd {
		m.cursor.Col = lineEnd
	} else {
		m.cursor.Col = m.cursor.PreferredCol
	}
	return m
}

// ClearSelection drops any active selection while keeping the current cursor.
func (m Model) ClearSelection() Model {
	m.selection = Selection{}
	return m
}

// HalfPageUp scrolls the viewport up by half a page.
func (m Model) HalfPageUp() Model { m.viewport.HalfPageUp(); return m }

// HalfPageDown scrolls the viewport down by half a page.
func (m Model) HalfPageDown() Model { m.viewport.HalfPageDown(); return m }

// ScrollUp scrolls up by n lines.
func (m Model) ScrollUp(n int) Model { m.viewport.ScrollUp(n); return m }

// ScrollDown scrolls down by n lines.
func (m Model) ScrollDown(n int) Model { m.viewport.ScrollDown(n); return m }

// GotoTop scrolls to the top of the viewport.
func (m Model) GotoTop() Model { m.viewport.GotoTop(); return m }

// GotoBottom scrolls to the bottom of the viewport.
func (m Model) GotoBottom() Model { m.viewport.GotoBottom(); return m }

// AtBottom reports whether the viewport is at the bottom.
func (m Model) AtBottom() bool { return m.viewport.AtBottom() }

// YOffset returns the current viewport vertical scroll offset.
func (m Model) YOffset() int { return m.viewport.YOffset() }

// GotoLiveEnd moves the history cursor to the end of the visible surface and
// scrolls to keep it visible.
func (m Model) GotoLiveEnd() Model {
	if len(m.lines) == 0 {
		m.cursor = Cursor{}
		return m
	}
	lastRow := len(m.lines) - 1
	m.cursor = Cursor{
		Row:          lastRow,
		Col:          lineWidth(m.lines[lastRow]),
		PreferredCol: lineWidth(m.lines[lastRow]),
		Visible:      true,
	}
	return m.ensureCursorVisible()
}

// CursorUp moves the cursor one visible row up, preserving preferred column.
func (m Model) CursorUp() Model {
	if !m.hasCursor() || m.cursor.Row == 0 {
		return m.ensureCursorVisible()
	}
	return m.moveCursorVertical(-1)
}

// CursorDown moves the cursor one visible row down, preserving preferred column.
func (m Model) CursorDown() Model {
	if !m.hasCursor() || m.cursor.Row >= len(m.lines)-1 {
		return m.ensureCursorVisible()
	}
	return m.moveCursorVertical(1)
}

// CursorLeft moves the cursor one visible cell to the left, crossing line
// boundaries when needed.
func (m Model) CursorLeft() Model {
	if !m.hasCursor() {
		return m
	}
	switch {
	case m.cursor.Col > 0:
		m.cursor.Col--
	case m.cursor.Row > 0:
		m.cursor.Row--
		m.cursor.Col = lineWidth(m.lines[m.cursor.Row])
	default:
		return m.ensureCursorVisible()
	}
	m.cursor.PreferredCol = m.cursor.Col
	return m.ensureCursorVisible()
}

// CursorRight moves the cursor one visible cell to the right, crossing line
// boundaries when needed.
func (m Model) CursorRight() Model {
	if !m.hasCursor() {
		return m
	}
	lineEnd := lineWidth(m.lines[m.cursor.Row])
	switch {
	case m.cursor.Col < lineEnd:
		m.cursor.Col++
	case m.cursor.Row < len(m.lines)-1:
		m.cursor.Row++
		m.cursor.Col = 0
	default:
		return m.ensureCursorVisible()
	}
	m.cursor.PreferredCol = m.cursor.Col
	return m.ensureCursorVisible()
}

// CursorWordLeft moves the cursor to the start of the previous visible word.
func (m Model) CursorWordLeft() Model {
	return m.moveCursorByWord(-1)
}

// CursorWordRight moves the cursor to the start of the next visible word.
func (m Model) CursorWordRight() Model {
	return m.moveCursorByWord(1)
}

// CursorLineStart moves the cursor to the start of the current visible line.
func (m Model) CursorLineStart() Model {
	if !m.hasCursor() {
		return m
	}
	m.cursor.Col = 0
	m.cursor.PreferredCol = 0
	return m.ensureCursorVisible()
}

// CursorLineEnd moves the cursor to the end of the current visible line.
func (m Model) CursorLineEnd() Model {
	if !m.hasCursor() {
		return m
	}
	m.cursor.Col = lineWidth(m.lines[m.cursor.Row])
	m.cursor.PreferredCol = m.cursor.Col
	return m.ensureCursorVisible()
}

// CursorPageUp moves the cursor up by roughly one viewport height.
func (m Model) CursorPageUp() Model {
	return m.moveCursorByPage(-1)
}

// CursorPageDown moves the cursor down by roughly one viewport height.
func (m Model) CursorPageDown() Model {
	return m.moveCursorByPage(1)
}

// SelectUp extends selection upward by one visible row.
func (m Model) SelectUp() Model {
	return m.extendSelection(func(next Model) Model { return next.CursorUp() })
}

// SelectDown extends selection downward by one visible row.
func (m Model) SelectDown() Model {
	return m.extendSelection(func(next Model) Model { return next.CursorDown() })
}

// SelectLeft extends selection one visible cell to the left.
func (m Model) SelectLeft() Model {
	return m.extendSelection(func(next Model) Model { return next.CursorLeft() })
}

// SelectRight extends selection one visible cell to the right.
func (m Model) SelectRight() Model {
	return m.extendSelection(func(next Model) Model { return next.CursorRight() })
}

// SelectWordLeft extends selection to a word start, or shrinks it to a word end.
func (m Model) SelectWordLeft() Model {
	if !m.hasCursor() {
		return m
	}
	if m.selection.Visible && m.selection.CursorEndExclusive && compareSurfacePositions(m.cursor, m.selection.Anchor) > 0 {
		m.cursor = m.wordEndBefore(m.cursor)
		if compareSurfacePositions(m.cursor, m.selection.Anchor) < 0 {
			m.cursor = m.selection.Anchor
		}
		m.cursor.PreferredCol = m.cursor.Col
		return m.ensureCursorVisible()
	}
	return m.extendSelection(func(next Model) Model { return next.CursorWordLeft() })
}

// SelectWordRight extends selection to a word end, or shrinks it to a word start.
func (m Model) SelectWordRight() Model {
	if !m.hasCursor() {
		return m
	}
	if !m.selection.Visible {
		m.selection = Selection{Anchor: m.cursor, Visible: true}
	}
	if compareSurfacePositions(m.cursor, m.selection.Anchor) < 0 {
		m.cursor = m.wordStartAfter(m.cursor)
		if compareSurfacePositions(m.cursor, m.selection.Anchor) > 0 {
			m.cursor = m.selection.Anchor
		}
	} else {
		m.cursor = m.wordEndAfter(m.cursor)
	}
	m.cursor.PreferredCol = m.cursor.Col
	m.selection.CursorEndExclusive = true
	return m.ensureCursorVisible()
}

// SelectLineStart extends selection to the start of the current visible line.
func (m Model) SelectLineStart() Model {
	return m.extendSelection(func(next Model) Model { return next.CursorLineStart() })
}

// SelectLineEnd extends selection to the end of the current visible line.
func (m Model) SelectLineEnd() Model {
	return m.extendSelection(func(next Model) Model { return next.CursorLineEnd() })
}

// SelectPageUp extends selection upward by one viewport page.
func (m Model) SelectPageUp() Model {
	return m.extendSelection(func(next Model) Model { return next.CursorPageUp() })
}

// SelectPageDown extends selection downward by one viewport page.
func (m Model) SelectPageDown() Model {
	return m.extendSelection(func(next Model) Model { return next.CursorPageDown() })
}

func (m Model) syncViewport(remapCursor bool) Model {
	if !m.viewportReady {
		return m
	}
	prevTop := m.viewport.YOffset()
	wasLiveEnd := remapCursor && m.cursorAtLiveEnd()
	cursorCoord, hasCursorCoord := surfaceCoord{}, false
	selectionCoord, hasSelectionCoord := surfaceCoord{}, false
	if remapCursor {
		cursorCoord, hasCursorCoord = m.cursorSurfaceCoord()
		selectionCoord, hasSelectionCoord = m.selectionSurfaceCoord()
	}
	ctx := m.viewportRenderContext()
	rendered := make([]string, 0, len(m.records))
	for i := range m.records {
		out, cached := renderRecordCached(m.records[i], ctx)
		m.records[i] = cached
		rendered = append(rendered, out)
	}
	content := joinRenderedForViewport(m.records, rendered)
	m.lines = splitHistoryLines(content)
	applyLayoutPrefixes(m.lines, m.records, rendered)
	m.viewport.SetContent(content)
	m.viewport.SetYOffset(clampViewportTop(prevTop, len(m.lines), m.viewport.Height()))
	m = m.syncCursor(remapCursor, wasLiveEnd, cursorCoord, hasCursorCoord)
	m = m.syncSelection(remapCursor, selectionCoord, hasSelectionCoord)
	return m
}

func applyLayoutPrefixes(lines []historyLine, records []viewRecord, rendered []string) {
	row := 0
	for i, renderedRecord := range rendered {
		if i > 0 && records[i].record.Kind != rec.KindSystem {
			row++
		}
		recordLines := strings.Split(strings.TrimSuffix(renderedRecord, "\n"), "\n")
		for lineIndex, line := range recordLines {
			if row >= len(lines) {
				return
			}
			lines[row].layoutPrefixWidth = rec.LayoutPrefixWidth(records[i].record, lineIndex, ansi.Strip(line))
			row++
		}
	}
}

func (m Model) moveCursorByWord(direction int) Model {
	if !m.hasCursor() {
		return m
	}
	if direction < 0 {
		m.cursor = m.wordStartBefore(m.cursor)
	} else {
		m.cursor = m.wordStartAfter(m.cursor)
	}
	m.cursor.PreferredCol = m.cursor.Col
	return m.ensureCursorVisible()
}

func (m Model) wordStartBefore(cursor Cursor) Cursor {
	next, ok := m.previousCell(cursor)
	for ok && m.cellIsSpace(next) {
		next, ok = m.previousCell(next)
	}
	if !ok {
		return Cursor{Row: 0, Col: 0, Visible: true}
	}
	cursor = next
	for {
		if cursor.Col == 0 {
			return cursor
		}
		next, ok = m.previousCell(cursor)
		if !ok || m.cellIsSpace(next) {
			return cursor
		}
		cursor = next
	}
}

func (m Model) wordStartAfter(cursor Cursor) Cursor {
	for !m.cellIsSpace(cursor) {
		next, ok := m.nextCell(cursor)
		if !ok {
			return m.lastCursorPosition()
		}
		cursor = next
	}
	for m.cellIsSpace(cursor) {
		next, ok := m.nextCell(cursor)
		if !ok {
			return m.lastCursorPosition()
		}
		cursor = next
	}
	return cursor
}

func (m Model) wordEndAfter(cursor Cursor) Cursor {
	for m.cellIsSpace(cursor) {
		next, ok := m.nextCell(cursor)
		if !ok {
			return m.lastCursorPosition()
		}
		cursor = next
	}
	for !m.cellIsSpace(cursor) {
		next, ok := m.nextCell(cursor)
		if !ok {
			return m.lastCursorPosition()
		}
		cursor = next
	}
	return cursor
}

func (m Model) wordEndBefore(cursor Cursor) Cursor {
	cursor = m.wordStartBefore(cursor)
	previous, ok := m.previousCell(cursor)
	for ok && m.cellIsSpace(previous) {
		previous, ok = m.previousCell(previous)
	}
	if !ok {
		return Cursor{Visible: true}
	}
	cursor, _ = m.nextCell(previous)
	return cursor
}

func (m Model) previousCell(cursor Cursor) (Cursor, bool) {
	if cursor.Col > 0 {
		cursor.Col--
		return cursor, true
	}
	if cursor.Row == 0 {
		return cursor, false
	}
	cursor.Row--
	cursor.Col = lineWidth(m.lines[cursor.Row])
	if cursor.Col > 0 {
		cursor.Col--
	}
	return cursor, true
}

func (m Model) nextCell(cursor Cursor) (Cursor, bool) {
	if cursor.Col < lineWidth(m.lines[cursor.Row]) {
		cursor.Col++
		return cursor, true
	}
	if cursor.Row >= len(m.lines)-1 {
		return cursor, false
	}
	cursor.Row++
	cursor.Col = 0
	return cursor, true
}

func (m Model) cellIsSpace(cursor Cursor) bool {
	if cursor.Row < 0 || cursor.Row >= len(m.lines) {
		return true
	}
	lineEnd := lineWidth(m.lines[cursor.Row])
	if cursor.Col >= lineEnd {
		return true
	}
	return strings.TrimSpace(visibleTextSlice(m.lines[cursor.Row].plain, cursor.Col, cursor.Col+1)) == ""
}

func (m Model) lastCursorPosition() Cursor {
	row := len(m.lines) - 1
	return Cursor{Row: row, Col: lineWidth(m.lines[row]), Visible: true}
}

func joinRenderedForViewport(records []viewRecord, rendered []string) string {
	if len(rendered) == 0 {
		return ""
	}
	// Add a blank line between records for readability in the viewport, but never
	// insert a blank line *above* a system record. This keeps system notices
	// tightly attached to the line above (e.g. command echo → status lines),
	// while still allowing spacing after the system block before the next record.
	//
	// NOTE: blank lines increase rendered height and can trigger scrolling earlier.
	var b strings.Builder
	for i, renderedRec := range rendered {
		if i > 0 {
			sep := "\n\n"
			if i < len(records) && records[i].record.Kind == rec.KindSystem {
				sep = "\n"
			}
			b.WriteString(sep)
		}
		b.WriteString(renderedRec)
	}
	return b.String()
}

func (m Model) hasCursor() bool {
	return m.cursor.Visible && len(m.lines) > 0
}

func (m Model) hasSelection() bool {
	return m.selection.Visible && len(m.lines) > 0
}

func (m Model) cursorAtLiveEnd() bool {
	if !m.hasCursor() || len(m.lines) == 0 {
		return true
	}
	lastRow := len(m.lines) - 1
	return m.cursor.Row == lastRow && m.cursor.Col == lineWidth(m.lines[lastRow])
}

type surfaceCoord struct {
	Advance int
}

func (m Model) syncCursor(remapCursor bool, wasLiveEnd bool, cursorCoord surfaceCoord, hasCursorCoord bool) Model {
	if len(m.lines) == 0 {
		m.cursor = Cursor{}
		return m
	}
	if m.shouldGotoLiveEnd(remapCursor, wasLiveEnd) {
		return m.GotoLiveEnd()
	}
	if remapCursor && hasCursorCoord {
		return m.setCursorFromSurfaceCoord(cursorCoord)
	}
	return m.clampCursorPosition()
}

func (m Model) cursorSurfaceCoord() (surfaceCoord, bool) {
	if !m.hasCursor() {
		return surfaceCoord{}, false
	}
	return m.positionSurfaceCoord(m.cursor.Row, m.cursor.Col)
}

func (m Model) selectionSurfaceCoord() (surfaceCoord, bool) {
	if !m.hasSelection() {
		return surfaceCoord{}, false
	}
	return m.positionSurfaceCoord(m.selection.Anchor.Row, m.selection.Anchor.Col)
}

func (m Model) setCursorFromSurfaceCoord(coord surfaceCoord) Model {
	if len(m.lines) == 0 {
		m.cursor = Cursor{}
		return m
	}
	row, col := m.positionFromSurfaceCoord(coord)
	m.cursor.Row = row
	m.cursor.Col = col
	m.cursor.PreferredCol = col
	m.cursor.Visible = true
	return m
}

func (m Model) syncSelection(remapCursor bool, selectionCoord surfaceCoord, hasSelectionCoord bool) Model {
	if len(m.lines) == 0 {
		m.selection = Selection{}
		return m
	}
	if !m.selection.Visible {
		return m
	}
	if remapCursor && hasSelectionCoord {
		row, col := m.positionFromSurfaceCoord(selectionCoord)
		m.selection.Anchor.Row = row
		m.selection.Anchor.Col = col
		m.selection.Anchor.PreferredCol = col
		m.selection.Anchor.Visible = true
		return m
	}
	m.selection.Anchor.Row = clampInt(m.selection.Anchor.Row, len(m.lines)-1)
	lineEnd := lineWidth(m.lines[m.selection.Anchor.Row])
	m.selection.Anchor.Col = clampInt(m.selection.Anchor.Col, lineEnd)
	m.selection.Anchor.PreferredCol = m.selection.Anchor.Col
	m.selection.Anchor.Visible = true
	return m
}

func (m Model) shouldGotoLiveEnd(remapCursor bool, wasLiveEnd bool) bool {
	if !m.cursor.Visible {
		return true
	}
	return remapCursor && wasLiveEnd
}

func (m Model) clampCursorPosition() Model {
	m.cursor.Row = clampInt(m.cursor.Row, len(m.lines)-1)
	lineEnd := lineWidth(m.lines[m.cursor.Row])
	m.cursor.Col = clampInt(m.cursor.Col, lineEnd)
	if m.cursor.PreferredCol < 0 {
		m.cursor.PreferredCol = 0
	}
	m.cursor.Visible = true
	return m
}

func (m Model) extendSelection(move func(Model) Model) Model {
	if !m.hasCursor() {
		return m
	}
	if !m.selection.Visible {
		m.selection = Selection{
			Anchor:  m.cursor,
			Visible: true,
		}
	}
	if m.selection.CursorEndExclusive && compareSurfacePositions(m.cursor, m.selection.Anchor) > 0 {
		if cursor, ok := m.previousCell(m.cursor); ok {
			m.cursor = cursor
		}
	}
	m = move(m)
	m.selection.CursorEndExclusive = false
	return m
}

func (m Model) positionSurfaceCoord(row, col int) (surfaceCoord, bool) {
	if len(m.lines) == 0 {
		return surfaceCoord{}, false
	}
	row = clampInt(row, len(m.lines)-1)
	col = clampInt(col, lineWidth(m.lines[row]))
	coord := surfaceCoord{}
	for i := 0; i < row && i < len(m.lines); i++ {
		coord.Advance += lineWidth(m.lines[i]) + 1
	}
	coord.Advance += col
	return coord, true
}

func (m Model) positionFromSurfaceCoord(coord surfaceCoord) (int, int) {
	if coord.Advance < 0 {
		coord.Advance = 0
	}
	row := 0
	remaining := coord.Advance
	for row < len(m.lines)-1 {
		rowSpan := lineWidth(m.lines[row]) + 1
		if remaining < rowSpan {
			break
		}
		remaining -= rowSpan
		row++
	}
	lineEnd := lineWidth(m.lines[row])
	col := clampInt(remaining, lineEnd)
	return row, col
}

func (m Model) moveCursorVertical(delta int) Model {
	if !m.hasCursor() {
		return m
	}
	nextRow := m.cursor.Row + delta
	if nextRow < 0 {
		nextRow = 0
	}
	if nextRow >= len(m.lines) {
		nextRow = len(m.lines) - 1
	}
	m.cursor.Row = nextRow
	lineEnd := lineWidth(m.lines[m.cursor.Row])
	if m.cursor.PreferredCol > lineEnd {
		m.cursor.Col = lineEnd
	} else {
		m.cursor.Col = m.cursor.PreferredCol
	}
	return m.ensureCursorVisible()
}

func (m Model) moveCursorByPage(direction int) Model {
	if !m.hasCursor() {
		return m
	}
	step := max(m.viewport.Height(), 1)
	if direction < 0 {
		step = -step
	}
	nextRow := m.cursor.Row + step
	if nextRow < 0 {
		nextRow = 0
	}
	if nextRow >= len(m.lines) {
		nextRow = len(m.lines) - 1
	}
	m.cursor.Row = nextRow
	lineEnd := lineWidth(m.lines[m.cursor.Row])
	if m.cursor.PreferredCol > lineEnd {
		m.cursor.Col = lineEnd
	} else {
		m.cursor.Col = m.cursor.PreferredCol
	}
	return m.ensureCursorVisible()
}

func (m Model) ensureCursorVisible() Model {
	if !m.hasCursor() {
		return m
	}
	top := m.viewport.YOffset()
	height := max(m.viewport.Height(), 1)
	bottom := top + height - 1

	switch {
	case m.cursor.Row < top:
		m.viewport.SetYOffset(m.cursor.Row)
	case m.cursor.Row > bottom:
		m.viewport.SetYOffset(m.cursor.Row - height + 1)
	}
	return m
}

func lineWidth(line historyLine) int {
	return ansi.StringWidth(line.plain)
}

func clampViewportTop(top, contentRows, height int) int {
	if top < 0 {
		top = 0
	}
	maxTop := 0
	if height > 0 && contentRows > height {
		maxTop = contentRows - height
	}
	if top > maxTop {
		top = maxTop
	}
	return top
}

func clampInt(n, maxValue int) int {
	if n < 0 {
		return 0
	}
	if n > maxValue {
		return maxValue
	}
	return n
}
