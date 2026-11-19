package approval

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/roomscript/coderoom/internal/interpreter"
	"github.com/roomscript/coderoom/internal/ui/palette"
)

// View renders a bounded request region above the approval options.
func (m Model) View() string {
	if !m.Active() {
		return ""
	}
	layout := m.layout()
	top := min(m.requestTop, layout.maxRequestTop())
	var lines []string
	lines = append(lines, layout.request[top:top+layout.requestRows]...)
	hint := m.overflowHint(layout, top)
	if layout.separator {
		lines = append(lines, hint)
	}
	lines = append(lines, layout.options...)
	if !layout.separator && hint != "" {
		last := len(lines) - 1
		if m.width <= 3 {
			lines[last] = ">" + ansi.Truncate(hint, m.width-1, "")
		} else {
			room := max(m.width-ansi.StringWidth(hint)-1, 0)
			lines[last] = ansi.Truncate(lines[last], room, "") + " " + hint
		}
	}
	return strings.Join(lines, "\n")
}

func (m Model) overflowHint(layout panelLayout, requestTop int) string {
	request := m.requestOverflow(layout, requestTop)
	options := overflowArrows(layout.optionsAbove, layout.optionsBelow)
	var parts []string
	if request != "" {
		parts = append(parts, "R"+request)
	}
	if options != "" {
		parts = append(parts, "O"+options)
	}
	if m.width > 0 && m.width < 12 {
		return compactOverflowHint(request, options, m.width)
	}
	if request != "" && m.width >= 30 && layout.requestRows > 0 {
		parts[0] += " PgUp/PgDn"
	}
	if options != "" && m.width >= 40 {
		parts[len(parts)-1] += " Up/Down"
	}
	return strings.Join(parts, " ")
}

func (m Model) requestOverflow(layout panelLayout, top int) string {
	if m.Compact() {
		return ""
	}
	return overflowArrows(top > 0, top+layout.requestRows < len(layout.request))
}

// compactOverflowHint leaves space for the selected label on narrow panels.
func compactOverflowHint(request, options string, width int) string {
	markers := ""
	if request != "" {
		markers += "…"
	}
	if options != "" {
		markers += "↕"
	}
	return ansi.Truncate(markers, width, "")
}

func overflowArrows(above, below bool) string {
	arrows := ""
	if above {
		arrows += "↑"
	}
	if below {
		arrows += "↓"
	}
	return arrows
}

type panelLayout struct {
	request      []string
	requestRows  int
	separator    bool
	options      []string
	optionsAbove bool
	optionsBelow bool
}

func (l panelLayout) maxRequestTop() int {
	return max(len(l.request)-l.requestRows, 0)
}

func (l panelLayout) height() int {
	h := l.requestRows + len(l.options)
	if l.separator {
		h++
	}
	return h
}

func (m Model) layout() panelLayout {
	if m.FileChange() {
		return m.fileLayout()
	}
	request := m.requestLines()
	options, selectedRow := m.optionLines()
	accent := lipgloss.NewStyle().Foreground(lipgloss.Color(palette.ColorApproval))
	for i, line := range options {
		options[i] = accent.Render(line)
	}
	if m.maxHeight == 0 {
		if m.Compact() {
			request = m.compactRequestLines(int(^uint(0) >> 1))
		}
		return panelLayout{request: request, requestRows: len(request), separator: true, options: options}
	}
	budget := m.maxHeight
	// Keep at least one option row visible, even in a tiny terminal.
	reserved := min(2, budget-1)
	optionRows := min(len(options), budget-reserved)
	top := min(selectedRow, len(options)-optionRows)
	optionsAbove := top > 0
	optionsBelow := top+optionRows < len(options)
	options = options[top : top+optionRows]
	remaining := budget - optionRows
	separator := remaining >= 1
	if separator {
		remaining--
	}
	if m.Compact() {
		request = m.compactRequestLines(remaining)
	}
	return panelLayout{
		request: request, requestRows: min(len(request), remaining),
		separator: separator, options: options,
		optionsAbove: optionsAbove, optionsBelow: optionsBelow,
	}
}

// compactRequestLines reserves the history hint before bounding summary and explanation.
// Paging belongs to history, so every displayed request row must be reachable
// without scrolling the approval body.
func (m Model) compactRequestLines(rows int) []string {
	request := m.requestLines()
	hint := m
	hint.alias = ""
	hint.summary = "Full command in history"
	if m.kind != interpreter.ApprovalCommandExecution {
		hint.summary = "Full request in history"
	}
	hintLines := hint.requestLines()
	if len(hintLines) >= rows {
		return hintLines[:rows]
	}
	count := rows - len(hintLines)
	request = m.boundRequestLines(request, count)
	if m.explanation != "" && len(request) < count {
		lines := m.bodyLines(escapeApprovalText(m.explanation))
		request = append(request, m.boundRequestLines(lines, count-len(request))...)
	} else if m.explanation != "" && count > 0 {
		request[count-1] = ansi.Truncate(request[count-1], max(m.width-1, 0), "") + "…"
	}
	return append(request, hintLines...)
}

func (m Model) boundRequestLines(lines []string, rows int) []string {
	if len(lines) <= rows {
		return lines
	}
	lines = lines[:rows]
	if rows > 0 {
		lines[rows-1] = ansi.Truncate(lines[rows-1], max(m.width-1, 0), "") + "…"
	}
	return lines
}

// fileLayout spends wrapped rows on whole paths, reserving options and overflow.
func (m Model) fileLayout() panelLayout {
	options, selected := m.optionLines()
	budget := m.maxHeight
	if budget == 0 {
		budget = int(^uint(0) >> 1)
	}
	optionRows := min(len(options), max(budget-1, 1))
	top := min(selected, len(options)-optionRows)
	layout := panelLayout{options: options[top : top+optionRows], optionsAbove: top > 0, optionsBelow: top+optionRows < len(options)}
	accent := lipgloss.NewStyle().Foreground(lipgloss.Color(palette.ColorApproval))
	for i := range layout.options {
		layout.options[i] = accent.Render(layout.options[i])
	}
	remaining := budget - optionRows
	for count := min(10, len(m.paths)); count >= 0; count-- {
		body := "File changes"
		for _, path := range m.paths[:count] {
			body += "\n" + path
		}
		if omitted := len(m.paths) - count; omitted > 0 {
			body += fmt.Sprintf("\nand %d more files — see history", omitted)
		}
		request := m
		request.summary = body
		lines := request.requestLines()
		if len(lines) <= remaining {
			layout.request, layout.requestRows = lines, len(lines)
			return layout
		}
	}
	// Tiny panels cannot fit the heading and summary. Prefer the omission count;
	// never display a partial filename as though it were the complete path.
	if remaining > 0 {
		text := "File changes — see history"
		if len(m.paths) > 0 {
			text = fmt.Sprintf("and %d more files — see history", len(m.paths))
		}
		layout.request = strings.Split(ansi.Wrap(text, m.width, ""), "\n")
		layout.requestRows = min(len(layout.request), remaining)
	}
	return layout
}

func (m Model) requestLines() []string {
	var lines []string
	if m.alias != "" {
		header := expandTabs(escapeApprovalText("● "+m.alias+":"), 0)
		style := lipgloss.NewStyle()
		if m.participantColor != "" {
			style = style.Foreground(lipgloss.Color(m.participantColor))
		}
		for line := range strings.SplitSeq(ansi.Wrap(header, m.width, ""), "\n") {
			lines = append(lines, style.Render(line))
		}
		if !m.Compact() {
			lines = append(lines, "")
		}
	}
	text := m.summary
	if !m.Compact() {
		if m.explanation != "" {
			text += "\n" + m.explanation
		}
		if m.evidence != "" {
			text += "\n" + m.evidence
		}
	}
	body := "⚡ Approval:\n" + escapeApprovalText(text) + "\nDecision: Waiting for you"
	if m.Compact() {
		body = "⚡ " + escapeApprovalText(m.summary)
	}
	return append(lines, m.bodyLines(body)...)
}

func (m Model) bodyLines(body string) []string {
	var lines []string
	indent, width := "  ", m.width
	if width > 2 {
		width -= 2
	} else if width > 0 {
		indent = ""
	}
	accent := lipgloss.NewStyle().Foreground(lipgloss.Color(palette.ColorApproval))
	for line := range strings.SplitSeq(ansi.Wrap(expandTabs(body, len(indent)), width, ""), "\n") {
		lines = append(lines, accent.Render(indent+line))
	}
	return lines
}

func (m Model) optionLines() ([]string, int) {
	var lines []string
	selectedRow := 0
	for i, opt := range m.options {
		prefix := "  "
		if i == m.selected {
			prefix = "> "
			selectedRow = len(lines)
		}
		// Wrap labels independently so continuation rows retain their indent.
		width := 0
		if m.width > 0 {
			width = max(m.width-2, 1)
		}
		label := strings.Split(ansi.Wrap(expandTabs(escapeApprovalText(formatOption(opt)), 2), width, ""), "\n")
		for j, line := range label {
			if j > 0 {
				prefix = "  "
			}
			if m.width > 0 && m.width <= 2 {
				lines = append(lines, ansi.Truncate(prefix+line, m.width, ""))
			} else {
				lines = append(lines, prefix+line)
			}
		}
	}
	return lines, selectedRow
}

func formatOption(opt interpreter.ApprovalOption) string {
	switch opt.ID {
	case "accept":
		return "accept"
	case "acceptForSession":
		return "accept for session"
	case "decline":
		return "decline"
	case "cancel":
		return "cancel"
	default:
		if opt.Label != "" {
			return opt.Label
		}
		return opt.ID
	}
}

// expandTabs uses eight-column tab stops before wrapping, so terminal tab
// expansion cannot introduce extra display rows. Prefix columns count too.
func expandTabs(text string, prefixWidth int) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		var out strings.Builder
		column := prefixWidth
		for j, part := range strings.Split(line, "\t") {
			if j > 0 {
				spaces := 8 - column%8
				out.WriteString(strings.Repeat(" ", spaces))
				column += spaces
			}
			out.WriteString(part)
			column += ansi.StringWidth(part)
		}
		lines[i] = out.String()
	}
	return strings.Join(lines, "\n")
}

// ExpandTabs applies the approval tab-stop contract to room evidence too.
func ExpandTabs(text string, prefixWidth int) string { return expandTabs(text, prefixWidth) }
