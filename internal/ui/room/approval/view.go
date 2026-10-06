package approval

import (
	"strings"

	"github.com/roomscript/coderoom/internal/interpreter"
)

// View renders the approval prompt and options.
func (m Model) View() string {
	if !m.Active() {
		return ""
	}
	var b strings.Builder
	b.WriteString(m.ask)
	b.WriteString("\n\n")
	for i, opt := range m.options {
		prefix := "  "
		if i == m.selected {
			prefix = "> "
		}
		b.WriteString(prefix)
		b.WriteString(formatOption(opt))
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
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
