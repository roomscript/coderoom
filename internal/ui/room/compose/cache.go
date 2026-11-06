package compose

import (
	"sort"
	"strings"
)

// wrapCache retains visual rows per logical line. Textarea owns editing; this
// cache only supplies row counts, cursor offsets, and the visible render window.
type wrapCache struct {
	text       string
	width      int
	lines      []string
	rows       [][]string
	offsets    []int
	renderRows int // excludes trailing padding introduced by wrapping
}

func (c *wrapCache) update(text string, width int) {
	if c.offsets != nil && c.text == text && c.width == width {
		return
	}
	lines := strings.Split(text, "\n")
	rows := c.wrapLines(lines, width)
	c.text, c.width, c.lines, c.rows = text, width, lines, rows
	c.offsets = make([]int, len(lines)+1)
	for i, row := range rows {
		c.offsets[i+1] = c.offsets[i] + len(row)
	}
	c.updateRenderRows()
}

func (c wrapCache) wrapLines(lines []string, width int) [][]string {
	rows := make([][]string, len(lines))
	// Reuse both sides of an edit, including when a newline is inserted/deleted.
	first, last := 0, 0
	if c.width == width {
		for first < min(len(lines), len(c.lines)) && lines[first] == c.lines[first] {
			rows[first] = c.rows[first]
			first++
		}
		for last < min(len(lines), len(c.lines))-first && lines[len(lines)-1-last] == c.lines[len(c.lines)-1-last] {
			rows[len(lines)-1-last] = c.rows[len(c.rows)-1-last]
			last++
		}
	}
	for i := first; i < len(lines)-last; i++ {
		rows[i] = wrapDisplayLine(lines[i], width)
	}
	return rows
}

func wrapDisplayLine(line string, width int) []string {
	wrapped := wrapTextareaLine([]rune(line), max(1, width))
	rows := make([]string, len(wrapped))
	for i, row := range wrapped {
		rows[i] = strings.TrimRight(string(row), " ")
	}
	return rows
}

func (c wrapCache) visualRows() int { return c.offsets[len(c.lines)] }

func (c *wrapCache) updateRenderRows() {
	c.renderRows = c.visualRows()
	if strings.HasSuffix(c.text, "\n") {
		return
	}
	for i := len(c.rows) - 1; i >= 0; i-- {
		for j := len(c.rows[i]) - 1; j >= 0; j-- {
			if strings.TrimSpace(c.rows[i][j]) != "" {
				return
			}
			c.renderRows--
		}
	}
	c.renderRows = 1
}

// visibleRows returns a new slice so prompt and cursor decoration cannot mutate
// cached rows. Offsets locate the first visible logical line without rewrapping.
func (c wrapCache) visibleRows(off, h int) ([]string, int) {
	off = clamp(off, 0, max(0, c.renderRows-h))
	out := make([]string, 0, h)
	i := sort.Search(len(c.lines), func(i int) bool { return c.offsets[i+1] > off })
	for row := off; row < min(c.renderRows, off+h); row++ {
		for i+1 < len(c.lines) && c.offsets[i+1] <= row {
			i++
		}
		out = append(out, c.rows[i][row-c.offsets[i]])
	}
	return out, off
}
