package record

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/roomscript/coderoom/internal/interpreter"
	roomstate "github.com/roomscript/coderoom/internal/room"
	"github.com/roomscript/coderoom/internal/ui/inlinefmt"
)

// Kind is an alias for room.Kind: room.Record stays the canonical record
// model, this package renders it rather than redefining it.
type Kind = roomstate.Kind

// Record is an alias for room.Record; see Kind.
type Record = roomstate.Record

// Record kind values, re-exported from room for callers that only import
// this package.
const (
	KindUserInput   = roomstate.KindUserInput
	KindAgentOutput = roomstate.KindAgentOutput
	KindSystem      = roomstate.KindSystem
	KindLog         = roomstate.KindLog
	KindReasoning   = roomstate.KindReasoning
	KindCommand     = roomstate.KindCommand
	KindFileChange  = roomstate.KindFileChange
)

// RenderMode controls how Record renders for UI consumers.
type RenderMode int

// Render mode values.
const (
	RenderViewport RenderMode = iota
	RenderTranscript
)

// RenderContext carries caller-provided rendering policy and dependencies.
type RenderContext struct {
	Key RenderKey
	// ColorForAlias returns a lipgloss color string for an active alias, or "".
	ColorForAlias func(alias string) string
}

// RenderKey is the comparable subset of RenderContext that affects output.
type RenderKey struct {
	Mode         RenderMode
	Width        int
	ColorVersion uint64
}

var (
	systemStyle = lipgloss.NewStyle().Faint(true)
	promptStyle = lipgloss.NewStyle().Bold(true)
	logStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
)

const (
	promptPrefix     = "❯ "
	logPrefix        = "▸ "
	agentBullet      = "● "
	handoffSourceTag = "↦ "
	reasoningBullet  = "◈ "
	commandBullet    = "$ "
	fileChangeBullet = "✎ "
	routingArrow     = "→ "
	agentBodyIndent  = "  "
)

// LayoutPrefixWidth reports presentation-only leading space on a rendered line.
func LayoutPrefixWidth(r Record, lineIndex int, plain string) int {
	if lineIndex < 2 || !strings.HasPrefix(plain, agentBodyIndent) {
		return 0
	}
	switch r.Kind {
	case KindAgentOutput, KindReasoning, KindCommand, KindFileChange:
		return ansi.StringWidth(agentBodyIndent)
	default:
		return 0
	}
}

// Render returns r rendered for the given context.
func Render(r Record, ctx RenderContext) string {
	width := ctx.Key.Width
	if ctx.Key.Mode == RenderTranscript {
		width = 0
	}
	colors := ctx.ColorForAlias
	if colors == nil {
		colors = func(string) string { return "" }
	}

	switch ctx.Key.Mode {
	case RenderTranscript:
		return renderTranscript(r, colors)
	default:
		return renderViewport(r, width, colors)
	}
}

func renderViewport(r Record, width int, colors func(string) string) string {
	switch r.Kind {
	case KindUserInput:
		return renderUserInput(r, width, colors)
	case KindAgentOutput:
		return renderAgentOutput(r, width, colors)
	case KindSystem:
		return systemStyle.Render(ansi.Wrap(r.Text, width, ""))
	case KindLog:
		return logStyle.Render(renderLogBody(r.Text, width))
	case KindReasoning:
		return renderReasoning(r, width, colors)
	case KindCommand:
		return renderCommand(r, width, colors)
	case KindFileChange:
		return renderFileChange(r, width, colors)
	}
	return r.Text
}

func renderTranscript(r Record, colors func(string) string) string {
	if r.Kind == KindCommand {
		return renderCommandTranscript(r, colors)
	}
	if r.Kind == KindFileChange {
		return renderFileChangeTranscript(r, colors)
	}
	return renderViewport(r, 0, colors)
}

func renderLogBody(body string, width int) string {
	if body == "" {
		return ""
	}
	parts := strings.Split(body, "\n")
	out := make([]string, 0, len(parts))
	for i, line := range parts {
		if i == len(parts)-1 && line == "" {
			continue
		}
		out = append(out, wrapLine(logPrefix+line, width, logPrefix))
	}
	return strings.Join(out, "\n")
}

func renderUserInput(r Record, width int, colors func(string) string) string {
	plain := promptPrefix + r.Text
	wrapped := wrapLine(plain, width, promptPrefix)
	if strings.HasPrefix(wrapped, promptPrefix) {
		wrapped = promptStyle.Render(promptPrefix) + wrapped[len(promptPrefix):]
	}
	if len(r.Routing) > 0 {
		wrapped += "\n" + renderRoutingFooter(r.Routing, colors)
	}
	if len(r.FailedRouting) > 0 {
		wrapped += "\n" + renderRoutingOutcome("failed: ", r.FailedRouting)
	}
	if len(r.UnsentRouting) > 0 {
		wrapped += "\n" + renderRoutingOutcome("not sent: ", r.UnsentRouting)
	}
	return wrapped
}

func renderAgentOutput(r Record, width int, colors func(string) string) string {
	color := colors(r.Alias)
	var header string
	var spanStyle lipgloss.Style
	bullet := agentBullet
	if r.HandoffSource {
		bullet = handoffSourceTag
	}
	if color != "" {
		spanStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(color))
		header = spanStyle.Render(bullet+r.Alias) + ":"
	} else {
		header = bullet + r.Alias + ":"
	}
	body := bodyFromRecord(r)
	if body == "" {
		return header
	}
	bodyText := body
	if color != "" {
		bodyText = inlinefmt.Format(bodyText, spanStyle)
	}
	wrapped := wrapLine(agentBodyIndent+bodyText, width, agentBodyIndent)
	return header + "\n\n" + wrapped
}

func renderReasoning(r Record, width int, colors func(string) string) string {
	color := colors(r.Alias)
	var headerStyle lipgloss.Style
	if color != "" {
		headerStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Faint(true)
	} else {
		headerStyle = lipgloss.NewStyle().Faint(true)
	}
	header := headerStyle.Render(reasoningBullet + r.Alias + " (thinking)")
	body := bodyFromRecord(r)
	if body == "" {
		return header
	}
	bodyText := body
	if color != "" {
		bodyText = inlinefmt.FormatWithStyles(bodyText, systemStyle, lipgloss.NewStyle().Foreground(lipgloss.Color(color)))
	} else {
		bodyText = systemStyle.Render(bodyText)
	}
	wrapped := wrapLine(agentBodyIndent+bodyText, width, agentBodyIndent)
	return header + "\n\n" + wrapped
}

func bodyFromRecord(r Record) string {
	if r.Msg == nil {
		return ""
	}
	return r.Text
}

func commandFieldsFromRecord(r Record) (cmd string, output string, exitCode *int) {
	if r.Msg == nil {
		return "", "", nil
	}
	c, ok := interpreter.CommandFromRecord(r)
	if !ok {
		return "", "", nil
	}
	return c.Command, c.Output, c.ExitCode
}

func renderCommand(r Record, width int, colors func(string) string) string {
	color := colors(r.Alias)
	cmd, output, exitCode := commandFieldsFromRecord(r)
	if cmd == "" {
		cmd = "…"
	}
	header := renderParticipantHeader(r.Alias, color)

	commandPrompt := renderCommandPrompt(color)
	commandLine, cmdTruncated := renderCommandSummaryLine(agentBodyIndent+commandPrompt, cmd, width)
	cmdHasNewline := strings.Contains(cmd, "\n")

	if output == "" && exitCode == nil {
		return renderPendingCommand(header, commandLine, cmdHasNewline || cmdTruncated)
	}

	var sb strings.Builder
	sb.WriteString(header)
	sb.WriteString("\n\n")
	sb.WriteString(commandLine)

	preview, remaining := renderCommandOutputPreview(output, width)
	if preview != "" {
		sb.WriteString("\n\n")
		sb.WriteString(preview)
	}

	if shouldRenderCommandHint(remaining, cmdHasNewline, cmdTruncated) {
		sb.WriteString("\n")
		sb.WriteString(logStyle.Render(commandDetailsHint(commandDetailsHintParams{
			cmdTruncated: cmdTruncated || cmdHasNewline,
			moreLines:    remaining,
		})))
	}

	if exitCode != nil {
		sb.WriteString("\n")
		sb.WriteString(logStyle.Render(fmt.Sprintf("%sexit %d", agentBodyIndent, *exitCode)))
	}

	return sb.String()
}

func renderParticipantHeader(alias string, color string) string {
	if color == "" {
		return agentBullet + alias + ":"
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Render(agentBullet+alias) + ":"
}

func renderCommandPrompt(color string) string {
	if color == "" {
		return commandBullet
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Render(commandBullet)
}

func renderFileChangePrompt(color string) string {
	if color == "" {
		return fileChangeBullet
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Render(fileChangeBullet)
}

func renderPendingCommand(header string, commandLine string, showHint bool) string {
	if !showHint {
		return header + "\n\n" + commandLine
	}
	return header + "\n\n" + commandLine + "\n" + logStyle.Render(commandDetailsHint(commandDetailsHintParams{
		cmdTruncated: true,
	}))
}

func shouldRenderCommandHint(moreLines int, cmdHasNewline bool, cmdTruncated bool) bool {
	return moreLines > 0 || cmdHasNewline || cmdTruncated
}

func renderCommandTranscript(r Record, colors func(string) string) string {
	color := colors(r.Alias)
	cmd, output, exitCode := commandFieldsFromRecord(r)
	if cmd == "" {
		cmd = "…"
	}
	header := renderParticipantHeader(r.Alias, color)
	commandPrompt := renderCommandPrompt(color)
	commandLine := renderCommandLine(agentBodyIndent+commandPrompt, cmd, 0)

	var sb strings.Builder
	sb.WriteString(header)
	sb.WriteString("\n\n")
	sb.WriteString(commandLine)

	if output != "" {
		sb.WriteString("\n\n")
		lines := strings.Split(strings.TrimRight(output, "\n"), "\n")
		for i, line := range lines {
			if i > 0 {
				sb.WriteString("\n")
			}
			sb.WriteString(agentBodyIndent + line)
		}
	}

	if exitCode != nil {
		sb.WriteString("\n")
		sb.WriteString(logStyle.Render(fmt.Sprintf("%sexit %d", agentBodyIndent, *exitCode)))
	}
	return sb.String()
}

const commandSummaryMaxCols = 120
const commandOutputPreviewLines = 3

func renderCommandSummaryLine(prefix string, cmd string, width int) (string, bool) {
	firstLine := cmd
	if i := strings.IndexByte(firstLine, '\n'); i >= 0 {
		firstLine = firstLine[:i] + " …"
	}
	if width <= 0 {
		return prefix + truncateColumns(firstLine, commandSummaryMaxCols), ansi.StringWidth(firstLine) > commandSummaryMaxCols
	}
	prefixWidth := ansi.StringWidth(prefix)
	contentWidth := max(width-prefixWidth, 1)
	maxCols := min(contentWidth, commandSummaryMaxCols)
	truncated := ansi.StringWidth(firstLine) > maxCols
	return prefix + truncateColumns(firstLine, maxCols), truncated
}

type commandDetailsHintParams struct {
	cmdTruncated bool
	moreLines    int
}

func commandDetailsHint(p commandDetailsHintParams) string {
	if p.moreLines > 0 {
		return fmt.Sprintf("%s(+%d more; Ctrl+O history, Ctrl+G open transcript)", agentBodyIndent, p.moreLines)
	}
	if p.cmdTruncated {
		return agentBodyIndent + "(command truncated; Ctrl+O history, Ctrl+G open transcript)"
	}
	return agentBodyIndent + "(Ctrl+O history, Ctrl+G open transcript)"
}

func renderCommandOutputPreview(body string, width int) (string, int) {
	if body == "" {
		return "", 0
	}
	lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
	if len(lines) == 0 {
		return "", 0
	}
	previewCount := min(len(lines), commandOutputPreviewLines)
	out := make([]string, 0, previewCount)
	maxCols := commandSummaryMaxCols
	if width > 0 {
		contentWidth := max(width-ansi.StringWidth(agentBodyIndent), 1)
		maxCols = min(maxCols, contentWidth)
	}
	for i := 0; i < previewCount; i++ {
		out = append(out, agentBodyIndent+truncateColumns(lines[i], maxCols))
	}
	return strings.Join(out, "\n"), len(lines) - previewCount
}

func truncateColumns(s string, maxCols int) string {
	if maxCols <= 0 {
		return ""
	}
	if ansi.StringWidth(s) <= maxCols {
		return s
	}
	if maxCols == 1 {
		return "…"
	}
	target := maxCols - 1
	var b strings.Builder
	b.Grow(len(s))
	cols := 0
	for _, r := range s {
		w := ansi.StringWidth(string(r))
		if cols+w > target {
			break
		}
		b.WriteRune(r)
		cols += w
	}
	return b.String() + "…"
}

func renderRoutingOutcome(label string, aliases []string) string {
	return strings.Repeat(" ", ansi.StringWidth(promptPrefix)) + label + strings.Join(aliases, ", ")
}

func renderRoutingFooter(aliases []string, colors func(string) string) string {
	parts := make([]string, len(aliases))
	indent := strings.Repeat(" ", ansi.StringWidth(promptPrefix))
	for i, alias := range aliases {
		color := colors(alias)
		if color != "" {
			parts[i] = indent + routingArrow + lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Render(alias)
		} else {
			parts[i] = indent + routingArrow + alias
		}
	}
	return strings.Join(parts, "    ")
}

func renderCommandLine(prefix string, cmd string, width int) string {
	if width <= 0 {
		return prefix + cmd
	}

	displayWidth := ansi.StringWidth(prefix)
	contentWidth := max(width-displayWidth, 1)
	wrapped := ansi.Wrap(cmd, contentWidth, "")
	parts := strings.Split(wrapped, "\n")

	indent := strings.Repeat(" ", displayWidth)
	for i := 1; i < len(parts); i++ {
		parts[i] = indent + parts[i]
	}
	return prefix + strings.Join(parts, "\n")
}

func wrapLine(text string, width int, prefix string) string {
	if width <= 0 {
		return text
	}
	return renderCommandLine(prefix, strings.TrimPrefix(text, prefix), width)
}
