package compose

import (
	"strings"

	"charm.land/bubbles/v2/textarea"
	"github.com/charmbracelet/x/ansi"
)

// promptWidth is computed (not a const) because we want the display width of
// the glyph as measured by the ANSI-aware width function.
var promptWidth = ansi.StringWidth("❯ ")

// View renders the input area.
func (m Model) View() string {
	// Don't rely on textarea's internal viewport rendering for multi-line input.
	// When pasting multiple logical lines, textarea's viewport can end up
	// scrolled such that only the last logical line is visible. Instead, render
	// visible rows from our per-logical-line wrap cache (see cache.go).
	h := m.input.Height()
	if h <= 0 {
		return ""
	}
	lines, off := m.wrapped.visibleRows(m.scrollOff, h)
	for i := range lines {
		if off+i == 0 {
			lines[i] = "❯ " + lines[i]
		} else {
			lines[i] = "  " + lines[i]
		}
	}
	if m.input.Focused() {
		row, col := m.cursorVisualRowCol()
		col += promptWidth
		if row >= off && row-off < len(lines) {
			lines = overlayBlockCursor(lines, row-off, col)
		}
	}
	return fitLinesToHeight(lines, h, 0)
}

func applyDecorations(input textarea.Model) textarea.Model {
	input.ShowLineNumbers = false
	input.SetPromptFunc(promptWidth, func(info textarea.PromptInfo) string {
		if info.LineNumber == 0 {
			return "❯ "
		}
		return "  "
	})
	return input
}

func fitLinesToHeight(lines []string, h int, scrollOff int) string {
	if h <= 0 {
		return ""
	}
	if len(lines) < h {
		padded := make([]string, h)
		copy(padded, lines)
		lines = padded
	} else if len(lines) > h {
		start := scrollOff
		if start < 0 {
			start = 0
		}
		maxStart := len(lines) - h
		if start > maxStart {
			start = maxStart
		}
		lines = lines[start : start+h]
	}
	return strings.Join(lines, "\n")
}

func overlayBlockCursor(lines []string, row int, col int) []string {
	out := make([]string, len(lines))
	copy(out, lines)

	r := []rune(out[row])
	if col > len(r) {
		pad := make([]rune, col-len(r))
		for i := range pad {
			pad[i] = ' '
		}
		r = append(r, pad...)
	}

	// Render a visible cursor without destroying the underlying character by
	// inverting the cell (SGR 7). This works well across terminals and keeps the
	// typed glyph readable.
	if col >= len(r) {
		r = append(r, ' ')
	}
	const invertOn = "\x1b[7m"
	const invertOff = "\x1b[0m"
	ch := r[col]
	out[row] = string(r[:col]) + invertOn + string(ch) + invertOff + string(r[col+1:])
	return out
}
