// Package inlinefmt implements lightweight, inline styling for agent output.
//
// Phase 1 keeps most formatting delimiters visible and applies
// participant-colored styling to the entire span. For bold and code spans, the
// delimiters are omitted from the rendered output.
package inlinefmt

import (
	"strings"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
)

type spanType int

const (
	spanCode spanType = iota
	spanBold
	spanItalic
	spanQuoteDoubleASCII
	spanQuoteSingleASCII
	spanQuotePaired
)

type candidate struct {
	typ   spanType
	open  string
	close string
}

// Priority order (see docs/design/pkg-ui-inlinefmt.md):
// 1) code, 2) bold, 3) italic, 4) ASCII double quote, 5) ASCII single quote,
// 6) paired quote delimiters.
var candidates = []candidate{
	{typ: spanCode, open: "```", close: "```"},
	{typ: spanCode, open: "`", close: "`"},
	{typ: spanBold, open: "**", close: "**"},
	{typ: spanItalic, open: "*", close: "*"},
	{typ: spanQuoteDoubleASCII, open: `"`, close: `"`},
	{typ: spanQuoteSingleASCII, open: `'`, close: `'`},
	{typ: spanQuotePaired, open: "“", close: "”"},
	{typ: spanQuotePaired, open: "‘", close: "’"},
	{typ: spanQuotePaired, open: "«", close: "»"},
	{typ: spanQuotePaired, open: "‹", close: "›"},
}

// Format applies lightweight inline formatting to s.
//
// Formatting is "flat": nested spans are not styled. The formatter scans
// left-to-right and styles multiple spans sequentially. At each opening
// delimiter position, it chooses the first delimiter type (by priority) that
// can be closed within the remaining text.
func Format(s string, base lipgloss.Style) string {
	if s == "" {
		return s
	}

	var out strings.Builder
	for i := 0; i < len(s); {
		openPos, ok := nextOpenerIndex(s, i)
		if !ok {
			out.WriteString(s[i:])
			break
		}
		if openPos > i {
			out.WriteString(s[i:openPos])
			i = openPos
		}

		rendered, advance, matched := tryMatchAt(s, i, base)
		if matched {
			out.WriteString(rendered)
			i += advance
			continue
		}

		consume := consumeUnclosableAt(s, i)
		out.WriteString(s[i : i+consume])
		i += consume
	}
	return out.String()
}

// FormatWithStyles applies inline formatting to s, rendering non-span text with
// baseStyle and matched spans with spanStyle.
//
// This is useful when the caller wants a "default" style for the whole string
// but wants emphasis spans to visually pop with a different style.
func FormatWithStyles(s string, baseStyle, spanStyle lipgloss.Style) string {
	if s == "" {
		return s
	}

	var out strings.Builder
	for i := 0; i < len(s); {
		openPos, ok := nextOpenerIndex(s, i)
		if !ok {
			out.WriteString(RenderLines(s[i:], baseStyle))
			break
		}
		if openPos > i {
			out.WriteString(RenderLines(s[i:openPos], baseStyle))
			i = openPos
		}

		rendered, advance, matched := tryMatchAt(s, i, spanStyle)
		if matched {
			out.WriteString(rendered)
			i += advance
			continue
		}

		consume := consumeUnclosableAt(s, i)
		out.WriteString(RenderLines(s[i:i+consume], baseStyle))
		i += consume
	}
	return out.String()
}

func nextOpenerIndex(s string, start int) (int, bool) {
	best := -1
	for _, c := range candidates {
		j := strings.Index(s[start:], c.open)
		if j < 0 {
			continue
		}
		j += start
		if best < 0 || j < best {
			best = j
		}
	}
	if best < 0 {
		return 0, false
	}
	return best, true
}

func tryMatchAt(s string, i int, base lipgloss.Style) (string, int, bool) {
	for _, c := range candidates {
		if !strings.HasPrefix(s[i:], c.open) {
			continue
		}
		end, ok := matchSpanEnd(s, i, c)
		if !ok {
			continue
		}
		span := s[i:end]
		return renderSpan(span, c, base), end - i, true
	}
	return "", 0, false
}

func renderSpan(span string, c candidate, base lipgloss.Style) string {
	switch c.typ {
	case spanCode:
		return RenderLines(stripDelimiters(span, c), base)
	case spanBold:
		return RenderLines(stripDelimiters(span, c), base.Bold(true))
	case spanItalic:
		return RenderLines(span, base.Italic(true))
	default:
		return RenderLines(span, base)
	}
}

// RenderLines styles each logical line separately, preserving newline separators
// without adding Lip Gloss block padding to inline fragments.
func RenderLines(text string, style lipgloss.Style) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = style.Render(line)
	}
	return strings.Join(lines, "\n")
}

func stripDelimiters(span string, c candidate) string {
	openLen := len(c.open)
	closeLen := len(c.close)
	if openLen+closeLen > len(span) {
		return span
	}
	return span[openLen : len(span)-closeLen]
}

func consumeUnclosableAt(s string, i int) int {
	consume := 0
	for _, c := range candidates {
		if strings.HasPrefix(s[i:], c.open) {
			consume = max(consume, len(c.open))
		}
	}
	if consume > 0 {
		return consume
	}
	_, size := utf8.DecodeRuneInString(s[i:])
	if size <= 0 {
		size = 1
	}
	return size
}

func matchSpanEnd(s string, openAt int, c candidate) (int, bool) {
	openLen := len(c.open)
	closeLen := len(c.close)
	searchFrom := openAt + openLen

	switch c.typ {
	case spanQuoteSingleASCII:
		// Apply the boundary rule: only treat ' as a quote at token boundaries.
		if !isSingleQuoteBoundaryBefore(s, openAt) {
			return 0, false
		}
		for j := strings.Index(s[searchFrom:], c.close); j >= 0; j = strings.Index(s[searchFrom:], c.close) {
			j += searchFrom
			// Reject empty spans like "''".
			if j == searchFrom {
				searchFrom = j + closeLen
				continue
			}
			if isSingleQuoteBoundaryAfter(s, j+closeLen) {
				return j + closeLen, true
			}
			searchFrom = j + closeLen
		}
		return 0, false
	default:
		j := strings.Index(s[searchFrom:], c.close)
		// Require non-empty content; prevents cases like "**bold*" being parsed
		// as italic "**" (empty span).
		if j <= 0 {
			return 0, false
		}
		return searchFrom + j + closeLen, true
	}
}

func isSingleQuoteBoundaryBefore(s string, quoteAt int) bool {
	if quoteAt == 0 {
		return true
	}
	// Note: this uses byte indexing. If the character before the quote is
	// multi-byte, prev will be a continuation byte and will not match any ASCII
	// boundary characters, which is acceptable for Phase 1.
	prev := s[quoteAt-1]
	switch prev {
	case ' ', '\t', '\n', '(', '[', '{':
		return true
	default:
		return false
	}
}

func isSingleQuoteBoundaryAfter(s string, afterQuote int) bool {
	if afterQuote >= len(s) {
		return true
	}
	next := s[afterQuote]
	switch next {
	case ' ', '\t', '\n', ',', '.', ':', ';', '!', '?', ')', ']', '}':
		return true
	default:
		return false
	}
}
