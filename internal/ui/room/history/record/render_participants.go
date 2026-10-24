package record

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/ui/inlinefmt"
	"github.com/roomscript/coderoom/internal/ui/toolbox"
)

// participantHeader names the /who table columns; the first is the state glyph.
var participantHeader = []string{"", "alias", "role", "backend", "model", "effort", "resume-id"}

const participantColumnGap = "  "

// renderParticipants renders a /who snapshot as a table, or as one block per
// participant when the table does not fit width. Width zero never stacks.
func renderParticipants(r Record, width int, colors func(participant.Alias) string) string {
	rows := make([][]string, len(r.Participants))
	for i, view := range r.Participants {
		rows[i] = participantCells(view)
	}
	widths := participantColumnWidths(append([][]string{participantHeader}, rows...))
	if width > 0 && participantTableWidth(widths) > width {
		return renderParticipantBlocks(r.Participants, rows, width, colors)
	}
	lines := []string{systemStyle.Render(formatParticipantRow(participantHeader, widths))}
	for i, row := range rows {
		lines = append(lines, systemRecordStyle(colors(r.Participants[i].Alias)).Render(formatParticipantRow(row, widths)))
	}
	return strings.Join(lines, "\n")
}

// participantCells returns one row in participantHeader order. Model and
// effort use the backend-managed labels; unknown values show "-".
func participantCells(view participant.View) []string {
	role, backend, model, effort := view.Role, view.Settings.Backend, "", ""
	if role == "" {
		role = "none"
	}
	if backend != "" {
		model, effort = view.Settings.ModelLabel(), view.Settings.EffortLabel()
	}
	return []string{
		toolbox.StatusGlyph(view.Status), view.Alias.Name, role,
		orDash(backend), orDash(model), orDash(effort), orDash(view.ResumeID),
	}
}

func orDash(value string) string {
	if value == "" {
		return "-"
	}
	return value
}

func participantColumnWidths(rows [][]string) []int {
	widths := make([]int, len(participantHeader))
	for _, row := range rows {
		for i, cell := range row {
			widths[i] = max(widths[i], ansi.StringWidth(cell))
		}
	}
	return widths
}

func participantTableWidth(widths []int) int {
	total := ansi.StringWidth(participantColumnGap) * (len(widths) - 1)
	for _, w := range widths {
		total += w
	}
	return total
}

func formatParticipantRow(cells []string, widths []int) string {
	padded := make([]string, len(cells))
	for i, cell := range cells {
		padded[i] = cell + strings.Repeat(" ", widths[i]-ansi.StringWidth(cell))
	}
	return strings.TrimRight(strings.Join(padded, participantColumnGap), " ")
}

// renderParticipantBlocks is the narrow fallback: a glyph and alias line, then
// one wrapped "name: value" line per remaining column.
func renderParticipantBlocks(views []participant.View, rows [][]string, width int, colors func(participant.Alias) string) string {
	blocks := make([]string, len(rows))
	for i, row := range rows {
		lines := []string{wrapLine(row[0]+" "+row[1], width, "")}
		for col := 2; col < len(row); col++ {
			lines = append(lines, wrapLine(agentBodyIndent+participantHeader[col]+": "+row[col], width, agentBodyIndent))
		}
		blocks[i] = inlinefmt.RenderLines(strings.Join(lines, "\n"), systemRecordStyle(colors(views[i].Alias)))
	}
	return strings.Join(blocks, "\n")
}
