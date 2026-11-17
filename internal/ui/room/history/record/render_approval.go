package record

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/ui/palette"
	"github.com/roomscript/coderoom/internal/ui/room/approval"
)

func renderApproval(r Record, width int, colors func(participant.Alias) string) string {
	header := renderParticipantHeader(approval.EscapeText(r.Alias.Name), colors(r.Alias))
	body := approval.EscapeText(r.Text + "\nDecision: " + r.Decision)
	// Expand tabs before wrapping so terminal tab stops cannot add hidden rows.
	body = approval.ExpandTabs(body, 2)
	lines := strings.Split(ansi.Wrap(body, max(width-2, 1), ""), "\n")
	if width == 0 {
		lines = strings.Split(body, "\n")
	}
	style := lipgloss.NewStyle().Foreground(lipgloss.Color(palette.ColorApproval))
	for i := range lines {
		lines[i] = style.Render(agentBodyIndent + lines[i])
	}
	return header + "\n\n" + strings.Join(lines, "\n")
}
