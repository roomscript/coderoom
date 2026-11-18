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

func TestApprovalDiffStylesAndWrapping(t *testing.T) {
	withANSIProfile(t, func() {
		name := strings.Repeat("long-name/", 12) + "file.go"
		r := Record{Kind: KindApproval, Text: "⚡ Approval: File changes\n" + name + "\n--- /project/file.go\n+++ /project/file.go\n@@ -1 +1 @@\n-old\n+new\x1b[2J", Decision: "Waiting for you"}
		for _, mode := range []RenderMode{RenderViewport, RenderTranscript} {
			got := Render(r, RenderContext{Key: RenderKey{Mode: mode, Width: 35}})
			for _, line := range []string{"--- /project/file.go", "+++ /project/file.go", "@@ -1 +1 @@", "-old", `+new\x1b[2J`} {
				if !strings.Contains(got, renderFileChangeDiffLine(line)) {
					t.Fatalf("mode %v missing styled diff %q: %q", mode, line, got)
				}
			}
			plain := ansi.Strip(got)
			if mode == RenderTranscript && !strings.Contains(plain, name) {
				t.Fatal("export wrapped or lost target")
			}
			if mode == RenderViewport {
				for _, line := range strings.Split(plain, "\n") {
					if ansi.StringWidth(line) > 35 {
						t.Fatalf("line exceeds viewport: %q", line)
					}
				}
			}
		}
	})
}

func TestCommandApprovalExport(t *testing.T) {
	command := "/opt/bin/git diff\n" + strings.Repeat("literal command evidence\n", 100) + "tail\x1b[2J"
	r := Record{Kind: KindApproval, Alias: participant.Alias{Name: "bill"}, Text: "⚡ Approval: shell command\nCommand: " + command + "\nWorking directory: unknown\nExplanation: supplied reason", Decision: "Withdrawn"}
	for _, mode := range []RenderMode{RenderViewport, RenderTranscript} {
		view := ansi.Strip(Render(r, RenderContext{Key: RenderKey{Mode: mode, Width: 60}}))
		for _, want := range []string{"/opt/bin/git diff", `tail\x1b[2J`, "Working directory: unknown", "Explanation: supplied reason", "Decision: Withdrawn"} {
			if !strings.Contains(view, want) {
				t.Fatalf("missing %q", want)
			}
		}
		if strings.Count(view, "literal command evidence") != 100 || strings.Contains(view, "\x1b") {
			t.Fatal("command evidence lost or controls executed")
		}
	}
}
