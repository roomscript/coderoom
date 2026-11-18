package approval

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/roomscript/coderoom/internal/interpreter"
	"github.com/roomscript/coderoom/internal/participant"
)

func TestCompactCommandApproval(t *testing.T) {
	req := interpreter.Approval{Kind: interpreter.ApprovalCommandExecution, Alias: participant.Alias{Name: "bill\x1b[2J"}, Summary: "git diff\x1b[2J", Prompt: "shortened", Details: "full evidence", Options: []interpreter.ApprovalOption{{ID: "accept"}, {ID: "decline"}}}
	for _, size := range []struct{ width, height int }{{80, 12}, {20, 6}, {3, 2}, {1, 1}} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			m := New().Set(req).SetSize(size.width, size.height)
			view := ansi.Strip(m.View())
			if m.Height() > size.height || strings.Contains(view, req.Details) || strings.Contains(view, req.Prompt) || strings.Contains(view, "\x1b") {
				t.Fatalf("bad compact view: %q", view)
			}
			assertCommandWidth(t, view, size.width)
			if size.width == 80 {
				for _, want := range []string{`bill\x1b[2J`, `git diff\x1b[2J`, "Full command in history", "> accept", "decline"} {
					if !strings.Contains(view, want) {
						t.Fatalf("missing %q: %q", want, view)
					}
				}
			}
		})
	}
}

func assertCommandWidth(t *testing.T, view string, width int) {
	t.Helper()
	for _, line := range strings.Split(view, "\n") {
		if ansi.StringWidth(line) > width {
			t.Fatalf("unbounded line: %q", line)
		}
	}
}

func TestLongCommandSummaryKeepsHistoryHint(t *testing.T) {
	for _, summary := range []string{strings.Repeat("executable", 100), "make " + strings.Repeat("target", 200)} {
		t.Run(summary[:10], func(t *testing.T) {
			req := interpreter.Approval{Kind: interpreter.ApprovalCommandExecution, Alias: participant.Alias{Name: "bill"}, Summary: summary, Options: []interpreter.ApprovalOption{{ID: "accept"}, {ID: "acceptForSession"}, {ID: "decline"}, {ID: "cancel"}}}
			m := New().Set(req).SetSize(80, 12)
			view := ansi.Strip(m.View())
			for _, want := range []string{"bill", "…", "Full command in history", "> accept", "accept for session", "decline", "cancel"} {
				if !strings.Contains(view, want) {
					t.Fatalf("missing %q: %q", want, view)
				}
			}
			if m.Height() > 12 {
				t.Fatalf("unbounded height: %d", m.Height())
			}
			assertCommandWidth(t, view, 80)
		})
	}
}
