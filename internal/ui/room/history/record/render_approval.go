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
	if r.FileApproval != nil {
		r.Text = fileApprovalPreview(r.FileApproval, width)
	}
	return renderApprovalTranscript(r, width, colors)
}

func renderApprovalTranscript(r Record, width int, colors func(participant.Alias) string) string {
	header := renderParticipantHeader(approval.EscapeText(r.Alias.Name), colors(r.Alias))
	body := approval.EscapeText(r.Text + "\nDecision: " + r.Decision)
	// Expand tabs before wrapping so terminal tab stops cannot add hidden rows.
	body = approval.ExpandTabs(body, 2)
	lines := strings.Split(body, "\n")
	style := lipgloss.NewStyle().Foreground(lipgloss.Color(palette.ColorApproval))
	for i := range lines {
		line := renderFileChangeDiffLine(lines[i])
		if width > 0 {
			line = ansi.Wrap(line, max(width-2, 1), "")
		}
		lines[i] = style.Render(agentBodyIndent + strings.ReplaceAll(line, "\n", "\n"+agentBodyIndent))
	}
	return header + "\n\n" + strings.Join(lines, "\n")
}
