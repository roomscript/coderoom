package compose

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// Compare the cached viewport with the original full-buffer renderer after
// edits, structural changes, navigation and resize. Includes wrap padding and
// Unicode because cursor offsets must continue matching textarea's behavior.
func TestCachedViewMatchesFullBuffer(t *testing.T) {
	for _, text := range []string{"", "   ", "one two three four\nfive\nsix seven\neight\nnine", "界界 hello 🌍\nsecond\n", "0123456789  \nlast   "} {
		for _, width := range []int{3, 12, 40} {
			t.Run(fmt.Sprintf("%q/width=%d", text, width), func(t *testing.T) {
				m := New().SetWidth(width).SetMaxHeightFromTotal(6).SetValue(text)
				assertFullBufferView(t, m)
				messages := []tea.Msg{
					tea.KeyPressMsg(tea.Key{Code: 'x', Text: "x"}),
					tea.KeyPressMsg(tea.Key{Code: tea.KeyHome, Mod: tea.ModCtrl}),
					tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}),
					tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter, Mod: tea.ModAlt}),
					tea.KeyPressMsg(tea.Key{Code: tea.KeyBackspace}),
					tea.PasteMsg{Content: "pasted words\nmore\n"},
					tea.KeyPressMsg(tea.Key{Code: tea.KeyUp}),
					tea.KeyPressMsg(tea.Key{Code: tea.KeyDelete}),
				}
				for _, msg := range messages {
					m, _ = m.Update(msg)
					assertFullBufferView(t, m)
				}
				m = m.SetWidth(width + 7)
				assertFullBufferView(t, m)
				m = m.Blur()
				assertFullBufferView(t, m)
				m = m.Reset()
				assertFullBufferView(t, m)
			})
		}
	}
}

func assertFullBufferView(t *testing.T, m Model) {
	t.Helper()
	text := m.Value()
	width := m.input.Width()
	lines := decoratePromptLines(renderBufferLines(text, width))
	if m.input.Focused() {
		row := 0
		logical := strings.Split(text, "\n")
		for _, line := range logical[:m.input.Line()] {
			row += len(wrapTextareaLine([]rune(line), width))
		}
		li := m.input.LineInfo()
		row += li.RowOffset
		if row < len(lines) {
			lines = overlayBlockCursor(lines, row, promptWidth+li.ColumnOffset)
		}
	}
	want := fitLinesToHeight(lines, m.Height(), m.scrollOff)
	if got := m.View(); got != want {
		t.Fatalf("view differs from full renderer:\ngot  %q\nwant %q", got, want)
	}
}

// Original renderer retained here as a compatibility reference.
func renderBufferLines(text string, contentW int) []string {
	if contentW <= 0 {
		contentW = 1
	}
	var out []string
	for line := range strings.SplitSeq(text, "\n") {
		wrapped := wrapTextareaLine([]rune(line), contentW)
		for _, wl := range wrapped {
			// textarea's wrap helper appends trailing spaces for cursor navigation
			// consistency; trim them for display so we don't render phantom blank
			// rows.
			out = append(out, strings.TrimRight(string(wl), " "))
		}
	}
	if len(out) == 0 {
		return []string{""}
	}
	// If the buffer does not end with a newline, drop trailing empty visual rows
	// introduced purely by wrapping/navigation padding.
	if !strings.HasSuffix(text, "\n") {
		for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
			out = out[:len(out)-1]
		}
		if len(out) == 0 {
			out = []string{""}
		}
	}
	return out
}

func decoratePromptLines(lines []string) []string {
	if len(lines) == 0 {
		return []string{"❯ "}
	}
	out := make([]string, len(lines))
	for i, line := range lines {
		if i == 0 {
			out[i] = "❯ " + line
		} else {
			out[i] = "  " + line
		}
	}
	return out
}
