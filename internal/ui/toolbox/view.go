package toolbox

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	rw "github.com/mattn/go-runewidth"
	"github.com/roomscript/coderoom/internal/participant"
)

const aliasMax = 10

// View renders the toolbox: the participant cells row.
func (m Model) View() string {
	return renderParticipantCells(m.width, m.now(), m.participants)
}

func activityTier(k participant.Status) int {
	switch k {
	case participant.StatusWorking:
		return 1
	case participant.StatusPreparing:
		return 2
	case participant.StatusKeepalive:
		return 3
	case participant.StatusStarting, participant.StatusAttached:
		return 4
	case participant.StatusCrashed:
		return 5
	case participant.StatusIdle:
		return 6
	default:
		return 99
	}
}

func formatElapsed(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	secs := int(d.Seconds())
	if secs < 60 {
		return fmt.Sprintf("%ds", secs)
	}
	mins := secs / 60
	sec := secs % 60
	if mins < 60 {
		return fmt.Sprintf("%dm%02ds", mins, sec)
	}
	h := mins / 60
	m := mins % 60
	return fmt.Sprintf("%dh%02dm", h, m)
}

func padOrTruncateToWidth(s string, width int) string {
	w := ansi.StringWidth(s)
	if w == width {
		return s
	}
	if w < width {
		return s + strings.Repeat(" ", width-w)
	}
	// Truncate by runes; good enough for aliases (mostly ASCII) and simple glyphs.
	var b strings.Builder
	curW := 0
	for _, r := range s {
		rw := ansi.StringWidth(string(r))
		if curW+rw > width {
			break
		}
		b.WriteRune(r)
		curW += rw
	}
	out := b.String()
	if ansi.StringWidth(out) < width {
		out += strings.Repeat(" ", width-ansi.StringWidth(out))
	}
	return out
}

// truncateToWidth truncates s to at most maxW display columns, appending "…"
// if truncation occurred.
func truncateToWidth(s string, maxW int) string {
	if ansi.StringWidth(s) <= maxW {
		return s
	}
	if maxW <= 1 {
		return ""
	}
	var b strings.Builder
	w := 0
	for _, r := range s {
		cw := rw.RuneWidth(r)
		if w+cw+1 > maxW { // reserve 1 column for the ellipsis
			break
		}
		b.WriteRune(r)
		w += cw
	}
	return b.String() + "…"
}

func cellWidth() int {
	// Conceptual width:
	// glyphWidth + 1 + aliasMax + 1 + len("(59m59s)")
	// We treat the glyph as width 2 for a stable conservative layout, since many
	// terminals render these as wide characters.
	glyphW := 2
	return glyphW + 1 + aliasMax + 1 + len("(59m59s)")
}

func renderParticipantCells(innerWidth int, now time.Time, ps []participant.View) string {
	w := cellWidth()
	if innerWidth <= 0 {
		return ""
	}
	n := max(innerWidth/w, 1)

	entries := make([]participant.View, 0, len(ps))
	for _, p := range ps {
		if p.Alias == "" {
			continue
		}
		entries = append(entries, p)
	}
	sort.Slice(entries, func(i, j int) bool {
		ti := activityTier(entries[i].Status)
		tj := activityTier(entries[j].Status)
		if ti != tj {
			return ti < tj
		}
		return entries[i].Alias < entries[j].Alias
	})

	visibleSlots := n
	overflow := 0
	if len(entries) > n {
		overflow = len(entries) - (n - 1)
		visibleSlots = n - 1
	}

	var cells []string
	for i := 0; i < min(visibleSlots, len(entries)); i++ {
		cells = append(cells, renderCell(entries[i], now, w))
	}
	if overflow > 0 {
		ov := fmt.Sprintf("+%d", overflow)
		cells = append(cells, padOrTruncateToWidth(ov, w))
	}
	for len(cells) < n {
		cells = append(cells, strings.Repeat(" ", w))
	}

	return strings.TrimRight(strings.Join(cells, ""), " ")
}

func renderCell(p participant.View, now time.Time, width int) string {
	glyph, showElapsed := participantGlyph(p.Status, now)
	elapsed := participantElapsedSuffix(p, now, showElapsed)
	// Reserve columns for glyph + separator + elapsed so truncation never
	// bites into the elapsed suffix.
	reserved := ansi.StringWidth(glyph) + 1 + ansi.StringWidth(elapsed)
	aliasAvail := min(aliasMax, max(0, width-reserved))
	base := glyph + " " + truncateToWidth(p.Alias, aliasAvail) + elapsed
	if p.Color != "" {
		colored := lipgloss.NewStyle().Foreground(lipgloss.Color(p.Color)).Render(base)
		if pad := width - ansi.StringWidth(base); pad > 0 {
			return colored + strings.Repeat(" ", pad)
		}
		return colored
	}
	return padOrTruncateToWidth(base, width)
}

func participantGlyph(status participant.Status, now time.Time) (string, bool) {
	switch status {
	case participant.StatusStarting, participant.StatusAttached:
		return "◌", true
	case participant.StatusPreparing:
		return "◐", true
	case participant.StatusKeepalive:
		return "◔", true
	case participant.StatusWorking:
		if int(now.Unix())%2 == 0 {
			return "⏹", true
		}
		return "◆", true
	case participant.StatusCrashed:
		return "✖", true
	case participant.StatusIdle:
		return "●", false
	default:
		return "●", false
	}
}

func participantElapsedSuffix(p participant.View, now time.Time, showElapsed bool) string {
	if !showElapsed {
		return ""
	}
	return " (" + formatElapsed(now.Sub(p.Since)) + ")"
}
