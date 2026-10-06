package room

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/roomscript/coderoom/internal/ui/room/history"
)

// View renders the room as:
//
//	header
//	history viewport
//	separator line
//	composer input
//
// It does not render outer chrome (toolbox, bottom padding, margins).
func (m Model) View() string {
	if !m.history.Ready() {
		return ""
	}
	header := renderHeader(m.history.Width(), m.history.ScrollStats(), m.historyLive)
	sep := labeledSeparator(m.history.Width(), m.separatorLabel(), m.composeIndicators())

	var sb strings.Builder
	sb.WriteString(header + "\n")
	for line := range strings.SplitSeq(m.renderHistoryView(), "\n") {
		sb.WriteString(line + "\n")
	}
	sb.WriteString(sep + "\n")
	if m.input.kind == inputApproval {
		for line := range strings.SplitSeq(m.input.approval.View(), "\n") {
			sb.WriteString(line + "\n")
		}
	} else {
		composeView := strings.TrimSuffix(m.input.compose.View(), "\n")
		if m.input.kind == inputStaged {
			faint := lipgloss.NewStyle().Faint(true)
			var out strings.Builder
			for line := range strings.SplitSeq(composeView, "\n") {
				out.WriteString(faint.Render(ansi.Strip(line)))
				out.WriteByte('\n')
			}
			composeView = strings.TrimSuffix(out.String(), "\n")
		}
		for line := range strings.SplitSeq(composeView, "\n") {
			sb.WriteString(line + "\n")
		}
		if m.input.kind == inputStaged && m.input.staged.status != "" {
			sb.WriteString(lipgloss.NewStyle().Faint(true).Render(m.input.staged.status) + "\n")
		}
	}
	sb.WriteString(m.bottomSeparator() + "\n")
	return strings.TrimRight(sb.String(), "\n")
}

func (m Model) renderHistoryView() string {
	if m.activeFocus == focusHistory {
		return m.history.View()
	}
	return m.history.ViewWithoutCursor()
}

func renderHeader(width int, stats history.ScrollStats, live bool) string {
	if width <= 0 {
		return ""
	}

	title := "coderoom"
	status := renderHeaderRight(stats, live)
	return headerLine(width, title, status)
}

func renderHeaderRight(stats history.ScrollStats, live bool) string {
	total := stats.ContentRows
	top := stats.Top
	h := stats.ViewportRows
	if h <= 0 {
		h = 1
	}

	start := 0
	end := 0
	if total > 0 {
		start = top + 1
		end = min(top+stats.ViewportRows, total)
	}

	screensAbove := ceilDiv(top, h)
	remainingBelow := max(0, total-(top+stats.ViewportRows))
	screensBelow := ceilDiv(remainingBelow, h)

	if live {
		return fmt.Sprintf("%d-%d/%d  (PgUp: %d)▲  LIVE", start, end, total, screensAbove)
	}
	return fmt.Sprintf("%d-%d/%d  (PgUp: %d)▲  (PgDn: %d)▼", start, end, total, screensAbove, screensBelow)
}

func ceilDiv(a, b int) int {
	if b <= 0 || a <= 0 {
		return 0
	}
	return (a + b - 1) / b
}

func truncateToWidth(s string, maxW int) string {
	if maxW <= 0 {
		return ""
	}
	if ansi.StringWidth(s) <= maxW {
		return s
	}
	var out strings.Builder
	curW := 0
	for _, r := range s {
		rw := ansi.StringWidth(string(r))
		if curW+rw > maxW {
			break
		}
		out.WriteRune(r)
		curW += rw
	}
	return out.String()
}

func headerLine(width int, title, status string) string {
	if width <= 0 {
		return ""
	}
	left := title + " "
	right := " " + status

	leftW := ansi.StringWidth(left)
	rightW := ansi.StringWidth(right)

	// If we can't fit both sides plus at least one dash, fall back to truncation.
	if leftW+rightW+1 > width {
		return truncateToWidth(left+status, width)
	}
	dashes := width - leftW - rightW
	return left + strings.Repeat("─", dashes) + right
}

func (m Model) separatorLabel() string {
	label := "compose"
	if m.activeFocus == focusHistory {
		label = "history"
	}
	if m.input.kind == inputApproval && m.activeFocus != focusHistory {
		label = "approval"
	}
	if !m.debug {
		return label
	}
	return label + " " + m.history.DebugLabel()
}

func (m Model) bottomSeparator() string {
	w := m.history.Width()
	if w <= 0 {
		return ""
	}
	suffix := ""
	if m.input.kind == inputCompose && m.input.compose.HasBelow() {
		suffix = "▼"
	}
	suffixW := len([]rune(suffix))
	return strings.Repeat("─", max(0, w-suffixW)) + suffix
}

func (m Model) composeIndicators() string {
	if m.input.kind == inputCompose && m.input.compose.HasAbove() {
		return "▲"
	}
	return ""
}

func labeledSeparator(width int, label, suffix string) string {
	if width <= 0 {
		return ""
	}
	suffixW := len([]rune(suffix)) // ▲▼ are single-display-width chars
	mid := " " + label + " "
	avail := width - suffixW
	if len(mid) >= avail {
		return strings.Repeat("─", max(avail, 0)) + suffix
	}
	leftCount := (avail - len(mid)) / 2
	rightCount := avail - len(mid) - leftCount
	return strings.Repeat("─", leftCount) + mid + strings.Repeat("─", rightCount) + suffix
}
