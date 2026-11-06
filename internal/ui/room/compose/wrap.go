package compose

import (
	"strings"
	"unicode"

	rw "github.com/mattn/go-runewidth"
	"github.com/rivo/uniseg"
)

// wrapTextareaLine implements the same word-wrapping behavior as
// charm.land/bubbles/v2/textarea (v1.x).
//
// Notably, it:
//   - wraps on whitespace
//   - tracks double-width runes
//   - intentionally appends a trailing space to wrapped lines to keep cursor
//     navigation consistent
func wrapTextareaLine(runes []rune, width int) [][]rune {
	lines := [][]rune{{}}
	word := []rune{}
	row := 0
	spaces := 0

	for _, r := range runes {
		if unicode.IsSpace(r) {
			spaces++
		} else {
			word = append(word, r)
		}

		if spaces > 0 {
			lines, row = flushWordAndSpaces(lines, row, word, spaces, width)
			word = nil
			spaces = 0
			continue
		}
		lines, row, word = maybeWrapLongWord(lines, row, word, width)
	}

	return appendFinalWord(lines, row, word, spaces, width)
}

func repeatSpaces(n int) []rune {
	return []rune(strings.Repeat(" ", n))
}

func flushWordAndSpaces(lines [][]rune, row int, word []rune, spaces int, width int) ([][]rune, int) {
	lineW := uniseg.StringWidth(string(lines[row]))
	wordW := uniseg.StringWidth(string(word))
	if lineW+wordW+spaces > width {
		row++
		lines = append(lines, []rune{})
		lines[row] = append(lines[row], word...)
		lines[row] = append(lines[row], repeatSpaces(spaces)...)
	} else {
		lines[row] = append(lines[row], word...)
		lines[row] = append(lines[row], repeatSpaces(spaces)...)
	}
	return lines, row
}

func maybeWrapLongWord(lines [][]rune, row int, word []rune, width int) ([][]rune, int, []rune) {
	if len(word) == 0 {
		return lines, row, word
	}
	lastCharLen := rw.RuneWidth(word[len(word)-1])
	if uniseg.StringWidth(string(word))+lastCharLen > width {
		if len(lines[row]) > 0 {
			row++
			lines = append(lines, []rune{})
		}
		lines[row] = append(lines[row], word...)
		return lines, row, nil
	}
	return lines, row, word
}

func appendFinalWord(lines [][]rune, row int, word []rune, spaces int, width int) [][]rune {
	lineW := uniseg.StringWidth(string(lines[row]))
	wordW := uniseg.StringWidth(string(word))
	if lineW+wordW+spaces >= width {
		lines = append(lines, []rune{})
		lines[row+1] = append(lines[row+1], word...)
		spaces++
		lines[row+1] = append(lines[row+1], repeatSpaces(spaces)...)
		return lines
	}
	lines[row] = append(lines[row], word...)
	spaces++
	lines[row] = append(lines[row], repeatSpaces(spaces)...)
	return lines
}
