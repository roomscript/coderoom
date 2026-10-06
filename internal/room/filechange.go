package room

import (
	"strings"

	"github.com/roomscript/coderoom/internal/agent"
)

// formatFileChangeBody renders a stable plain-text representation of a file
// change set, used to populate Record.Text. UI rendering packages should
// read Record.Text rather than calling this directly.
func formatFileChangeBody(changes []agent.FileChange) string {
	if len(changes) == 0 {
		return ""
	}
	var sb strings.Builder
	for i, ch := range changes {
		if i > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString("=== ")
		sb.WriteString(ch.Path)
		if ch.ChangeKind != "" {
			sb.WriteString(" (")
			sb.WriteString(ch.ChangeKind)
			sb.WriteString(")")
		}
		sb.WriteString("\n")
		sb.WriteString(ch.Diff)
		if !strings.HasSuffix(ch.Diff, "\n") {
			sb.WriteString("\n")
		}
	}
	return sb.String()
}
