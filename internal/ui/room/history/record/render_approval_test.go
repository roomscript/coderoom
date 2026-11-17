package record

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/roomscript/coderoom/internal/participant"
)

func TestApprovalEvidenceRenderingAndExport(t *testing.T) {
	r := Record{Kind: KindApproval, Alias: participant.Alias{Name: "bill\x1b[2J"}, Text: "⚡ Approval: File changes\nfile\n" + strings.Repeat("+full evidence\n", 100) + "+tail\x1b[2J\u202e\xff", Decision: "Approved by you · once"}
	for _, mode := range []RenderMode{RenderViewport, RenderTranscript} {
		view := ansi.Strip(Render(r, RenderContext{Key: RenderKey{Mode: mode, Width: 60}}))
		for _, want := range []string{"⚡ Approval: File changes", "● bill\\x1b[2J:", "+tail\\x1b[2J\\u202e\\xff", "Decision: Approved by you · once"} {
			if !strings.Contains(view, want) {
				t.Fatalf("mode %v missing %q", mode, want)
			}
		}
		if strings.Count(view, "+full evidence") != 100 || strings.ContainsAny(view, "\x1b\u202e") {
			t.Fatal("evidence lost or terminal controls executed")
		}
	}
}
