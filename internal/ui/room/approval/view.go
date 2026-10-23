package approval

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/roomscript/coderoom/internal/interpreter"
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
	request := overflowArrows(requestTop > 0, requestTop+layout.requestRows < len(layout.request))
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
	request := strings.Split(ansi.Wrap(expandTabs(escapeApprovalText(m.ask), 0), m.width, ""), "\n")
	options, selectedRow := m.optionLines()
	if m.maxHeight == 0 {
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
	return panelLayout{
		request: request, requestRows: min(len(request), remaining),
		separator: separator, options: options,
		optionsAbove: optionsAbove, optionsBelow: optionsBelow,
	}
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
