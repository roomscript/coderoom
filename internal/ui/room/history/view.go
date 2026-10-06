package history

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rivo/uniseg"
	rec "github.com/roomscript/coderoom/internal/ui/room/history/record"
)

// View renders the viewport with an optional row-number overlay. The output is
// always exactly viewport.Height lines joined by newlines, with no trailing
// newline, so the outer layout gets a stable height.
func (m Model) View() string {
	return m.view(true)
}

// ViewWithoutCursor renders the viewport while suppressing the history cursor.
func (m Model) ViewWithoutCursor() string {
	return m.view(false)
}

func (m Model) view(showCursor bool) string {
	if !m.viewportReady {
		return ""
	}
	viewportLines := m.viewportLines(showCursor)
	lines := make([]string, m.viewport.Height())
	for i := range lines {
		if i < len(viewportLines) {
			lines[i] = viewportLines[i]
		}
		if m.debugRowNums {
			lines[i] = strconv.Itoa(i+1) + ":" + lines[i]
		}
	}
	return strings.Join(lines, "\n")
}

func (m Model) viewportLines(showCursor bool) []string {
	if len(m.lines) == 0 || m.viewport.Height() <= 0 {
		return nil
	}
	top := clampViewportRow(m.viewport.YOffset(), len(m.lines))
	bottom := min(top+m.viewport.Height(), len(m.lines))
	out := make([]string, 0, bottom-top)
	for row := top; row < bottom; row++ {
		out = append(out, m.renderViewportRow(row, showCursor))
	}
	return out
}

func clampViewportRow(top, lineCount int) int {
	if top < 0 {
		return 0
	}
	if top > lineCount {
		return lineCount
	}
	return top
}

func (m Model) renderViewportRow(row int, showCursor bool) string {
	line := m.lines[row].raw
	rangeStart, rangeEnd, selected := m.selectionColumnsForRow(row)
	cursorCol, hasCursor := m.viewportCursor(row, showCursor)
	if !selected && !hasCursor {
		return line
	}
	return renderSelectionLine(line, rangeStart, rangeEnd, selected, cursorCol, hasCursor, m.viewport.Width())
}

func (m Model) viewportCursor(row int, showCursor bool) (int, bool) {
	if showCursor && m.cursor.Visible && row == m.cursor.Row {
		return m.cursor.Col, true
	}
	return 0, false
}

func renderCursorLine(raw string, cursorCol int, width int) string {
	return renderSelectionLine(raw, 0, 0, false, cursorCol, true, width)
}

func renderSelectionLine(raw string, selStart, selEnd int, hasSelection bool, cursorCol int, hasCursor bool, width int) string {
	if width <= 0 {
		return raw
	}

	cells, suffix := splitStyledCells(raw)
	lineWidth := styledCellsWidth(cells)
	cursorCol = normalizeCursorColumn(cursorCol, lineWidth, width)
	selStart, selEnd = normalizeSelectionColumns(selStart, selEnd, lineWidth)
	return lipgloss.NewStyle().Width(width).Render(renderDecoratedCells(cells, suffix, selStart, selEnd, hasSelection, cursorCol, hasCursor, lineWidth))
}

type styledCell struct {
	raw   string
	width int
}

func splitStyledCells(raw string) ([]styledCell, string) {
	var cells []styledCell
	var pending strings.Builder
	var suffix strings.Builder

	for i := 0; i < len(raw); {
		if seqLen := ansiSequenceLength(raw[i:]); seqLen > 0 {
			pending.WriteString(raw[i : i+seqLen])
			i += seqLen
			continue
		}

		cluster, size, width := nextVisibleCluster(raw[i:])
		if size == 0 {
			break
		}

		if width == 0 {
			pending.WriteString(cluster)
		} else {
			cells = append(cells, styledCell{
				raw:   pending.String() + cluster,
				width: width,
			})
			pending.Reset()
		}
		i += size
	}

	suffix.WriteString(pending.String())
	return cells, suffix.String()
}

func styledCellsWidth(cells []styledCell) int {
	total := 0
	for _, cell := range cells {
		total += cell.width
	}
	return total
}

func nextVisibleCluster(s string) (cluster string, size int, width int) {
	gr := uniseg.NewGraphemes(s)
	if !gr.Next() {
		return "", 0, 0
	}
	cluster = gr.Str()
	size = len(cluster)
	width = ansi.StringWidth(cluster)
	return cluster, size, width
}

func ansiSequenceLength(s string) int {
	if len(s) == 0 || s[0] != '\x1b' {
		return 0
	}
	if len(s) == 1 {
		return 1
	}
	return ansiSequenceBodyLength(s)
}

func normalizeCursorColumn(cursorCol, lineWidth, width int) int {
	cursorCol = clampInt(cursorCol, lineWidth)
	if cursorCol == lineWidth && lineWidth >= width && lineWidth > 0 {
		return lineWidth - 1
	}
	return cursorCol
}

func renderDecoratedCells(cells []styledCell, suffix string, selStart, selEnd int, hasSelection bool, cursorCol int, hasCursor bool, lineWidth int) string {
	if hasCursor && cursorCol == lineWidth {
		return renderDecoratedLineWithEndCursor(cells, suffix, selStart, selEnd, hasSelection)
	}
	return renderDecoratedInsideLine(cells, suffix, selStart, selEnd, hasSelection, cursorCol, hasCursor)
}

func renderDecoratedLineWithEndCursor(cells []styledCell, suffix string, selStart, selEnd int, hasSelection bool) string {
	cursorStyle := lipgloss.NewStyle().Reverse(true)
	line := renderDecoratedInsideLine(cells, suffix, selStart, selEnd, hasSelection, 0, false)
	return line + cursorStyle.Render(" ")
}

func renderDecoratedInsideLine(cells []styledCell, suffix string, selStart, selEnd int, hasSelection bool, cursorCol int, hasCursor bool) string {
	var b strings.Builder
	col := 0
	for _, cell := range cells {
		next := col + cell.width
		renderStyledCell(&b, cell, col, next, selStart, selEnd, hasSelection, cursorCol, hasCursor)
		col = next
	}
	b.WriteString(suffix)
	return b.String()
}

func renderStyledCell(b *strings.Builder, cell styledCell, col, next, selStart, selEnd int, hasSelection bool, cursorCol int, hasCursor bool) {
	inSelection := hasSelection && col < selEnd && next > selStart
	hasCursorCell := hasCursor && cursorCol >= col && cursorCol < next
	switch {
	case inSelection:
		b.WriteString("\x1b[48;5;238m")
		if hasCursorCell {
			b.WriteString("\x1b[7m")
		}
		b.WriteString(cell.raw)
		if hasCursorCell {
			b.WriteString("\x1b[27m")
		}
		b.WriteString("\x1b[49m")
	default:
		if hasCursorCell {
			b.WriteString("\x1b[7m")
			b.WriteString(cell.raw)
			b.WriteString("\x1b[27m")
			return
		}
		b.WriteString(cell.raw)
	}
}

func normalizeSelectionColumns(start, end, lineWidth int) (int, int) {
	start = clampInt(start, lineWidth)
	end = clampInt(end, lineWidth)
	if start > end {
		start, end = end, start
	}
	return start, end
}

func ansiSequenceBodyLength(s string) int {
	switch s[1] {
	case '[':
		return ansiCSISequenceLength(s)
	case ']':
		return ansiOSCSequenceLength(s)
	default:
		return ansiSingleEscapeLength(s)
	}
}

func ansiCSISequenceLength(s string) int {
	for i := 2; i < len(s); i++ {
		if s[i] >= 0x40 && s[i] <= 0x7e {
			return i + 1
		}
	}
	return len(s)
}

func ansiOSCSequenceLength(s string) int {
	for i := 2; i < len(s); i++ {
		if s[i] == '\a' {
			return i + 1
		}
		if s[i] == '\x1b' && i+1 < len(s) && s[i+1] == '\\' {
			return i + 2
		}
	}
	return len(s)
}

func ansiSingleEscapeLength(s string) int {
	_, size := utf8.DecodeRuneInString(s[1:])
	if size <= 0 {
		return 1
	}
	return 1 + size
}

func (m Model) selectionColumnsForRow(row int) (int, int, bool) {
	if !m.hasSelection() {
		return 0, 0, false
	}
	start, end := m.selectionSpan()
	if row < start.Row || row > end.Row {
		return 0, 0, false
	}
	lineEnd := lineWidth(m.lines[row])
	switch {
	case start.Row == end.Row:
		return start.Col, end.Col, start.Col != end.Col
	case row == start.Row:
		return start.Col, lineEnd, start.Col != lineEnd
	case row == end.Row:
		return 0, end.Col, end.Col != 0
	default:
		return 0, lineEnd, lineEnd != 0
	}
}

func (m Model) selectionSpan() (Cursor, Cursor) {
	comparison := compareSurfacePositions(m.cursor, m.selection.Anchor)
	if comparison == 0 {
		return m.cursor, m.selection.Anchor
	}
	if comparison < 0 {
		return m.cursor, m.advanceSelectionCursor(m.selection.Anchor)
	}
	if m.selection.CursorEndExclusive {
		return m.selection.Anchor, m.cursor
	}
	return m.selection.Anchor, m.advanceSelectionCursor(m.cursor)
}

func (m Model) advanceSelectionCursor(cursor Cursor) Cursor {
	if cursor.Row < 0 || cursor.Row >= len(m.lines) {
		return cursor
	}
	lineEnd := lineWidth(m.lines[cursor.Row])
	if cursor.Col >= lineEnd {
		return cursor
	}
	cursor.Col++
	cursor.PreferredCol = cursor.Col
	return cursor
}

func compareSurfacePositions(left, right Cursor) int {
	switch {
	case left.Row < right.Row:
		return -1
	case left.Row > right.Row:
		return 1
	case left.Col < right.Col:
		return -1
	case left.Col > right.Col:
		return 1
	default:
		return 0
	}
}

// SelectedText returns the active visible selection as plain text.
func (m Model) SelectedText() (string, bool) {
	if !m.hasSelection() {
		return "", false
	}
	start, end := m.selectionSpan()
	if compareSurfacePositions(start, end) == 0 {
		return "", false
	}
	parts := make([]string, 0, end.Row-start.Row+1)
	for row := start.Row; row <= end.Row; row++ {
		selStart, selEnd, ok := m.selectionColumnsForRow(row)
		if !ok {
			parts = append(parts, "")
			continue
		}
		selStart = max(selStart, m.lines[row].layoutPrefixWidth)
		selected := visibleTextSlice(m.lines[row].plain, selStart, selEnd)
		parts = append(parts, strings.TrimRight(selected, " \t"))
	}
	return strings.Join(parts, "\n"), true
}

func visibleTextSlice(raw string, startCol, endCol int) string {
	if startCol >= endCol || raw == "" {
		return ""
	}
	var b strings.Builder
	col := 0
	for i := 0; i < len(raw); {
		cluster, size, width := nextVisibleCluster(raw[i:])
		if size == 0 {
			break
		}
		next := col + width
		if width == 0 {
			if b.Len() > 0 {
				b.WriteString(cluster)
			}
			i += size
			continue
		}
		if col >= endCol {
			break
		}
		if next > startCol && col < endCol {
			b.WriteString(cluster)
		}
		col = next
		i += size
	}
	return b.String()
}

// RenderedContent returns the raw rendered records joined by newlines; useful
// for checking all history content regardless of scroll position.
func (m Model) RenderedContent() string {
	ctx := m.viewportRenderContext()
	parts := make([]string, 0, len(m.records))
	for i := range m.records {
		out, cached := renderRecordCached(m.records[i], ctx)
		m.records[i] = cached
		parts = append(parts, out)
	}
	return strings.Join(parts, "\n")
}

// PlainText returns the ANSI-stripped, double-newline-joined rendered records
// for transcript export.
func (m Model) PlainText() string {
	parts := make([]string, 0, len(m.records))
	ctx := rec.RenderContext{
		Key:           rec.RenderKey{Mode: rec.RenderTranscript, ColorVersion: m.colorVersion},
		ColorForAlias: m.resolveColor,
	}
	for _, r := range m.records {
		parts = append(parts, rec.Render(r.record, ctx))
	}
	return strings.Join(parts, "\n\n")
}

// DebugLabel returns a compact summary string for the separator label.
func (m Model) DebugLabel() string {
	content := m.RenderedContent()
	contentLines := 0
	first := ""
	if content != "" {
		contentLines = strings.Count(content, "\n") + 1
		first = content
		if i := strings.IndexByte(first, '\n'); i >= 0 {
			first = first[:i]
		}
		first = strings.TrimSpace(ansi.Strip(first))
		if len(first) > 24 {
			first = first[:24]
		}
	}

	viewContent := strings.TrimSuffix(m.viewport.View(), "\n")
	viewFirst := ""
	viewWho := 0
	viewLines := 0
	if viewContent != "" {
		viewLines = strings.Count(viewContent, "\n") + 1
		viewWho = strings.Count(ansi.Strip(viewContent), "❯ /who")
		viewFirst = viewContent
		if i := strings.IndexByte(viewFirst, '\n'); i >= 0 {
			viewFirst = viewFirst[:i]
		}
		viewFirst = strings.TrimSpace(ansi.Strip(viewFirst))
		if len(viewFirst) > 24 {
			viewFirst = viewFirst[:24]
		}
	}

	return fmt.Sprintf("y=%d h=%d rec=%d ln=%d first=%s viewFirst=%s viewWho=%d viewLn=%d",
		m.viewport.YOffset(), m.viewport.Height(),
		len(m.records), contentLines,
		first, viewFirst,
		viewWho, viewLines)
}

// DebugSummary returns a multi-line string summarising the viewport top for
// the /debugview command.
func (m Model) DebugSummary() string {
	view := ansi.Strip(strings.TrimSuffix(m.viewport.View(), "\n"))
	var lines []string
	if view != "" {
		lines = strings.Split(view, "\n")
	}
	if len(lines) > 8 {
		lines = lines[:8]
	}
	parts := []string{
		fmt.Sprintf("  y=%d h=%d rec=%d", m.viewport.YOffset(), m.viewport.Height(), len(m.records)),
		"  viewTop:",
	}
	for _, line := range lines {
		parts = append(parts, "    "+line)
	}
	return strings.Join(parts, "\n")
}
