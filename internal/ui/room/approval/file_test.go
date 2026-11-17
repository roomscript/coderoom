package approval

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/roomscript/coderoom/internal/interpreter"
	"github.com/roomscript/coderoom/internal/participant"
)

func TestCompactFileApproval(t *testing.T) {
	paths := make([]string, 12)
	for i := range paths {
		paths[i] = fmt.Sprintf("file-%02d.go", i)
	}
	req := interpreter.Approval{Kind: interpreter.ApprovalFileChange, Alias: participant.Alias{Name: "bill"}, Prompt: "approve", Paths: paths, Details: "+evidence stays in history", Options: []interpreter.ApprovalOption{{ID: "accept"}, {ID: "decline"}}}
	m := New().Set(req).SetSize(80, 30)
	view := ansi.Strip(m.View())
	if strings.Contains(view, req.Details) || strings.Contains(view, paths[10]) || !strings.Contains(view, "and 2 more files — see history") || !strings.Contains(view, "● bill:") || !strings.Contains(view, "File changes") {
		t.Fatalf("compact approval = %q", view)
	}
	for _, path := range paths[:10] {
		if !strings.Contains(view, "\n  "+path+"\n") {
			t.Fatalf("path missing or packed: %q", path)
		}
	}
}

func TestCompactFileApprovalSmallPanels(t *testing.T) {
	req := interpreter.Approval{Kind: interpreter.ApprovalFileChange, Alias: participant.Alias{Name: "bill"}, Options: []interpreter.ApprovalOption{{ID: "accept"}, {ID: "decline"}}}
	for _, size := range []struct{ width, height int }{{40, 12}, {12, 8}, {3, 2}, {1, 1}} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			req.Paths = []string{strings.Repeat("long", 200), "second"}
			m := New().Set(req).SetSize(size.width, size.height)
			view := ansi.Strip(m.View())
			if m.Height() > size.height || strings.Contains(view, "long") {
				t.Fatalf("unbounded or partially displayed path: %q", view)
			}
			if size.width == 40 && !strings.Contains(view, "and 2 more files — see history") {
				t.Fatalf("omission count lost: %q", view)
			}
		})
	}
}
