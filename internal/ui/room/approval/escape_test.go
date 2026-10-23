package approval

import (
	"fmt"
	"strings"
	"testing"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/roomscript/coderoom/internal/interpreter"
)

func TestEscapeApprovalText(t *testing.T) {
	tests := []struct {
		name, in, want string
	}{
		{"printable", "rm -rf ./build", "rm -rf ./build"},
		{"newline and tab", "a\n\tb", "a\n\tb"},
		{"carriage return", "safe\rrm -rf /", `safe\rrm -rf /`},
		{"crlf", "a\r\nb", "a\\r\nb"},
		{"backspace", "rm\b\b", `rm\b\b`},
		{"csi erase", "\x1b[2J", `\x1b[2J`},
		{"other c0 and del", "\x00\x07\x7f", `\x00\x07\x7f`},
		{"c1 csi", "\u009b2J", `\u009b2J`},
		{"invalid byte", "a\x9bb", `a\x9bb`},
		{"invalid sequence keeps every byte", "\xe2\x28\xa1", `\xe2(\xa1`},
		{"literal backslash", `printf '\x1b'`, `printf '\x1b'`},
		{"bidi override", "a\u202eb\u202c", `a\u202eb\u202c`},
		{"bidi isolate", "\u2066a\u2069", `\u2066a\u2069`},
		{"invisible characters", "a\u200bb\u2060c\u00add\ufeff", `a\u200bb\u2060c\u00add\ufeff`},
		{"zero-width joiner", "\U0001f468\u200d\U0001f469", "\U0001f468\\u200d\U0001f469"},
		{"tag character", "a\U000e0041", `a\U000e0041`},
		{"right-to-left letters", "\u05e9\u05dc\u05d5\u05dd", "\u05e9\u05dc\u05d5\u05dd"},
		{"unicode", "界 é é", "界 é é"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := escapeApprovalText(tt.in); got != tt.want {
				t.Fatalf("escapeApprovalText(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

var hostileText = []struct {
	name, raw, shown string
}{
	{"carriage return", "ls\rrm -rf /", `ls\rrm -rf /`},
	{"backspace", "rm -rf /\b\b\b\bls", `rm -rf /\b\b\b\bls`},
	{"cursor movement", "rm\x1b[1A\x1b[2Kls", `rm\x1b[1A\x1b[2Kls`},
	{"erase display", "rm\x1b[2J", `rm\x1b[2J`},
	{"conceal", "ls \x1b[8mrm -rf /\x1b[0m", `ls \x1b[8mrm -rf /\x1b[0m`},
	{"color", "\x1b[31mrm\x1b[0m", `\x1b[31mrm\x1b[0m`},
	{"osc hyperlink", "\x1b]8;;http://x\x1b\\ls\x1b]8;;\x1b\\", `\x1b]8;;http://x\x1b\ls\x1b]8;;\x1b\`},
	{"osc title", "\x1b]0;title\x07rm", `\x1b]0;title\x07rm`},
	{"c1 csi", "rm\u009b2J", `rm\u009b2J`},
	{"incomplete sequence", "rm\x1b[", `rm\x1b[`},
	{"trojan source", "ls \u202e/ fr- mr\u202c", `ls \u202e/ fr- mr\u202c`},
	{"zero-width space", "rm\u200b -rf /", `rm\u200b -rf /`},
}

func TestView_escapesUntrustedText(t *testing.T) {
	for _, tt := range hostileText {
		t.Run(tt.name, func(t *testing.T) {
			views := map[string]struct {
				approval interpreter.Approval
				want     string
			}{
				"request": {
					interpreter.Approval{Prompt: tt.raw, Options: []interpreter.ApprovalOption{{ID: "accept"}}},
					tt.shown + "\n\n> accept",
				},
				"label": {
					interpreter.Approval{Prompt: "approve?", Options: []interpreter.ApprovalOption{{ID: "custom", Label: tt.raw}}},
					"approve?\n\n> " + tt.shown,
				},
				"fallback id": {
					interpreter.Approval{Prompt: "approve?", Options: []interpreter.ApprovalOption{{ID: tt.raw}}},
					"approve?\n\n> " + tt.shown,
				},
			}
			for path, v := range views {
				m := New().Set(v.approval).SetSize(200, 20)
				if got := m.View(); got != v.want {
					t.Fatalf("%s: view = %q, want %q", path, got, v.want)
				}
				assertNoRawControls(t, m.View())
			}
		})
	}
}

func TestView_escapedTextStaysBoundedAndPageable(t *testing.T) {
	prompt := strings.Repeat("\x1b[2K\rrm -rf /tmp/x\n", 8) + "tail\x1b[8m"
	controlID := "custom\x1b[8m"
	for _, size := range []struct{ width, height int }{{8, 4}, {20, 6}, {40, 10}} {
		t.Run(fmt.Sprintf("%dx%d", size.width, size.height), func(t *testing.T) {
			approval := interpreter.Approval{
				Prompt:  prompt,
				Options: []interpreter.ApprovalOption{{ID: "accept"}, {ID: controlID}},
			}
			m := New().Set(approval).SetSize(size.width, size.height)
			m, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
			for i := 0; i < 100; i++ {
				assertNoRawControls(t, m.View())
				assertFitsPanel(t, m, size.width, size.height)
				m, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyPgDown}))
			}
			assertShowsRequestTail(t, m)
			// Rewrapping on resize must keep the paged tail visible and bounded.
			m = m.SetSize(size.width+7, size.height+2)
			assertNoRawControls(t, m.View())
			assertFitsPanel(t, m, size.width+7, size.height+2)
			assertShowsRequestTail(t, m)
			if m.Ask() != prompt || m.Options()[1].ID != controlID {
				t.Fatalf("stored request or options changed: %q %#v", m.Ask(), m.Options())
			}
			choice, ok := m.SelectedOption()
			if !ok || choice.OptionID != controlID {
				t.Fatalf("choice = %#v, ok = %v", choice, ok)
			}
		})
	}
}

func assertShowsRequestTail(t *testing.T, m Model) {
	t.Helper()
	request := m.layout().request
	if tail := request[len(request)-1]; !strings.HasSuffix(tail, "8m") || !strings.Contains(m.View(), tail) {
		t.Fatalf("request tail is not visible: %q", m.View())
	}
}

func assertFitsPanel(t *testing.T, m Model, width, height int) {
	t.Helper()
	view := m.View()
	if rows := strings.Count(view, "\n") + 1; rows != m.Height() || rows > height {
		t.Fatalf("rendered rows=%d, measured=%d, budget=%d", rows, m.Height(), height)
	}
	for _, line := range strings.Split(view, "\n") {
		if ansi.StringWidth(line) > width {
			t.Fatalf("line exceeds width %d: %q", width, line)
		}
	}
}

// assertNoRawControls checks today's unstyled View output: only layout LF may
// reach the terminal as a control or format character.
func assertNoRawControls(t *testing.T, view string) {
	t.Helper()
	for _, r := range view {
		if r != '\n' && (unicode.IsControl(r) || unicode.Is(unicode.Cf, r)) {
			t.Fatalf("raw control or format character %U reached the view: %q", r, view)
		}
	}
}
