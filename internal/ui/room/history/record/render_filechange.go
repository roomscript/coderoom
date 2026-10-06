package record

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/roomscript/coderoom/internal/interpreter"
	"github.com/roomscript/coderoom/internal/ui/palette"
)

func fileChangeFieldsFromRecord(r Record) (status string, changes []interpreter.FileChange, body string) {
	if r.Msg == nil {
		return "", nil, ""
	}
	c, ok := interpreter.FileChangesFromRecord(r)
	if !ok {
		return "", nil, ""
	}
	return c.Status, c.Changes, r.Text
}

const fileChangePreviewLines = 8

func renderFileChange(r Record, width int, colors func(string) string) string {
	color := colors(r.Alias)
	header := renderParticipantHeader(r.Alias, color)
	fileChangePrompt := renderFileChangePrompt(color)

	var sb strings.Builder
	sb.WriteString(header)

	if isPendingFileChange(r) {
		sb.WriteString("\n\n")
		sb.WriteString(agentBodyIndent + fileChangePrompt + "…")
		return sb.String()
	}

	appendFileChangeList(&sb, r, fileChangePrompt)
	appendFileChangeBodyPreview(&sb, r, width)
	appendFileChangePatchStatus(&sb, r)

	return sb.String()
}

func isPendingFileChange(r Record) bool {
	status, changes, body := fileChangeFieldsFromRecord(r)
	return len(changes) == 0 && body == "" && status == ""
}

func appendFileChangeList(sb *strings.Builder, r Record, fileChangePrompt string) {
	_, changes, _ := fileChangeFieldsFromRecord(r)
	changes = uniqueFileChanges(changes)
	if len(changes) == 0 {
		return
	}
	sb.WriteString("\n\n")
	sb.WriteString(agentBodyIndent)
	sb.WriteString(fileChangePrompt)
	sb.WriteString("files:")
	for _, ch := range changes {
		sb.WriteString("\n")
		sb.WriteString(agentBodyIndent)
		sb.WriteString("- ")
		if ch.ChangeKind != "" {
			sb.WriteString(ch.ChangeKind)
			sb.WriteString(" ")
		}
		sb.WriteString(ch.Path)
	}
}

func appendFileChangeBodyPreview(sb *strings.Builder, r Record, width int) {
	_, _, body := fileChangeFieldsFromRecord(r)
	if body == "" {
		return
	}
	sb.WriteString("\n\n")
	lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
	previewCount := min(len(lines), fileChangePreviewLines)
	maxCols := fileChangePreviewMaxCols(width)
	for i := 0; i < previewCount; i++ {
		if i > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString(agentBodyIndent)
		sb.WriteString(renderFileChangeDiffLine(truncateColumns(lines[i], maxCols)))
	}
	appendFileChangeBodyMoreHint(sb, len(lines)-previewCount)
}

func fileChangePreviewMaxCols(width int) int {
	maxCols := commandSummaryMaxCols
	if width <= 0 {
		return maxCols
	}
	contentWidth := max(width-ansi.StringWidth(agentBodyIndent), 1)
	return min(maxCols, contentWidth)
}

func appendFileChangeBodyMoreHint(sb *strings.Builder, remainingLines int) {
	if remainingLines <= 0 {
		return
	}
	sb.WriteString("\n")
	sb.WriteString(logStyle.Render(fmt.Sprintf(
		"%s(+%d more; Ctrl+O history, Ctrl+G open transcript)",
		agentBodyIndent,
		remainingLines,
	)))
}

func appendFileChangePatchStatus(sb *strings.Builder, r Record) {
	status, _, _ := fileChangeFieldsFromRecord(r)
	if status == "" {
		return
	}
	sb.WriteString("\n")
	sb.WriteString(logStyle.Render(fmt.Sprintf("%s%s", agentBodyIndent, status)))
}

func renderFileChangeTranscript(r Record, colors func(string) string) string {
	color := colors(r.Alias)
	header := renderParticipantHeader(r.Alias, color)
	fileChangePrompt := renderFileChangePrompt(color)
	var sb strings.Builder
	sb.WriteString(header)

	status, changes, body := fileChangeFieldsFromRecord(r)
	changes = uniqueFileChanges(changes)
	if len(changes) > 0 {
		sb.WriteString("\n\n")
		sb.WriteString(agentBodyIndent)
		sb.WriteString(fileChangePrompt)
		sb.WriteString("files:")
		for _, ch := range changes {
			sb.WriteString("\n")
			sb.WriteString(agentBodyIndent)
			sb.WriteString("- ")
			if ch.ChangeKind != "" {
				sb.WriteString(ch.ChangeKind)
				sb.WriteString(" ")
			}
			sb.WriteString(ch.Path)
		}
	}

	if body != "" {
		sb.WriteString("\n\n")
		lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
		for i, line := range lines {
			if i > 0 {
				sb.WriteString("\n")
			}
			sb.WriteString(agentBodyIndent + renderFileChangeDiffLine(line))
		}
	}

	if status != "" {
		sb.WriteString("\n")
		sb.WriteString(logStyle.Render(fmt.Sprintf("%s%s", agentBodyIndent, status)))
	}

	return sb.String()
}

var (
	fileChangeDiffHeaderStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(palette.ColorFileChangeDiffHeader)).Faint(true)
	fileChangeHunkStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color(palette.ColorFileChangeHunk)).Bold(true)
	fileChangeAddStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color(palette.ColorFileChangeAdd))
	fileChangeDelStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color(palette.ColorFileChangeDel))
	fileChangeMetaStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color(palette.ColorFileChangeMeta)).Faint(true)
	fileChangeSectionStyle    = lipgloss.NewStyle().Bold(true)
)

func renderFileChangeDiffLine(line string) string {
	switch {
	case strings.HasPrefix(line, "=== "):
		return fileChangeSectionStyle.Render(line)
	case strings.HasPrefix(line, "diff --git "),
		strings.HasPrefix(line, "index "),
		strings.HasPrefix(line, "--- "),
		strings.HasPrefix(line, "+++ "):
		return fileChangeDiffHeaderStyle.Render(line)
	case strings.HasPrefix(line, "@@ "):
		return fileChangeHunkStyle.Render(line)
	case strings.HasPrefix(line, "+"):
		return fileChangeAddStyle.Render(line)
	case strings.HasPrefix(line, "-"):
		return fileChangeDelStyle.Render(line)
	case strings.HasPrefix(line, `\ No newline at end of file`):
		return fileChangeMetaStyle.Render(line)
	default:
		return line
	}
}

type fileChangeKey struct {
	kind string
	path string
}

func uniqueFileChanges(changes []interpreter.FileChange) []interpreter.FileChange {
	if len(changes) <= 1 {
		return changes
	}
	seen := make(map[fileChangeKey]struct{}, len(changes))
	out := make([]interpreter.FileChange, 0, len(changes))
	for _, ch := range changes {
		key := fileChangeKey{kind: ch.ChangeKind, path: ch.Path}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, ch)
	}
	return out
}
