package record

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/roomscript/coderoom/internal/room"
)

func TestApprovalFileCounts(t *testing.T) {
	tests := []struct{ name, format, text, want string }{
		{"hunk", "unified", "--- a\n+++ b\n@@ -1,2 +1,2 @@\n--- content\n+++ content\n same\n", "+1 -1"},
		{"creation", "unified", "@@ -0,0 +1,2 @@\n+a\n+b\n", "+2 -0"},
		{"deletion", "unified", "@@ -1 +0,0 @@\n-old\n", "+0 -1"},
		{"multiple hunks", "unified", "@@ -1 +1 @@\n-a\n+b\n@@ -9 +9 @@\n-c\n+d\n\\ No newline at end of file\n", "+2 -2"},
		{"incomplete", "unified", "@@ -1,2 +1 @@\n-a\n+b\n", "counts unavailable"},
		{"unsupported", "unified", "@@\n-a\n+b\n", "counts unavailable"},
		{"extra content", "unified", "@@ -1 +1 @@\n-a\n+b\n+extra", "counts unavailable"},
		{"trailing new header", "unified", "@@ -1 +1 @@\n-a\n+b\n+++ extra", "counts unavailable"},
		{"trailing old header", "unified", "@@ -1 +1 @@\n-a\n+b\n--- extra", "counts unavailable"},
		{"trailing header pair", "unified", "@@ -1 +1 @@\n-a\n+b\n--- extra\n+++ extra", "counts unavailable"},
		{"trailing section", "unified", "@@ -1 +1 @@\n-a\n+b\ndiff --git a/x b/x\n", "counts unavailable"},
		{"incomplete header pair", "unified", "--- a\n@@ -1 +1 @@\n-a\n+b\n", "counts unavailable"},
		{"unpaired new header", "unified", "+++ b\n@@ -1 +1 @@\n-a\n+b\n", "counts unavailable"},
		{"git metadata", "unified", "diff --git a/x b/x\nindex 123..456 100644\n--- a/x\n+++ b/x\n@@ -1 +1 @@\n-a\n+b\n", "+1 -1"},
		{"recorded codex addition", "addition", "ok\n", "+1 -0"},
		{"empty addition", "addition", "", "+0 -0"},
		{"addition blank line", "addition", "ok\n\n", "+2 -0"},
		{"addition without newline", "addition", "ok", "+1 -0"},
		{"missing", "", "", "counts unavailable"},
		{"empty write", "content", "", "0 supplied content lines"},
		{"write", "content", "a\n\n", "2 supplied content lines"},
		{"snippet newline", "snippet", "@@ replace matching occurrence @@\n-old\n-\n+new\n+\n", "snippet +1 -1"},
		{"replace all", "snippet", "@@ replace all matching occurrences @@\n-old\n+new\n", "snippet +1 -1"},
		{"empty snippet", "snippet", "@@ replace matching occurrence @@\n", "snippet +0 -0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := approvalFileCounts(room.ApprovalFile{Format: tt.format, Text: tt.text})
			if !strings.HasPrefix(got, tt.want) {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFileApprovalPreviewAndTranscript(t *testing.T) {
	p := &room.FileApproval{Summary: "File changes", Explanation: "supplied reason", Supplemental: true}
	for i := 0; i < 12; i++ {
		p.Files = append(p.Files, room.ApprovalFile{Path: fmt.Sprintf("file-%02d", i), ChangeKind: "write", Format: "content", Text: strings.Repeat("body\n", 10) + "tail\x1b[2J"})
	}
	r := Record{Kind: KindApproval, FileApproval: p, Text: "complete request\nscope\n" + strings.Repeat("body\n", 100) + "tail\x1b[2J", Decision: "Waiting for you"}
	for _, width := range []int{1, 20, 120} {
		view := ansi.Strip(Render(r, RenderContext{Key: RenderKey{Width: width}}))
		compact := strings.ReplaceAll(view, "\n", "")
		compact = strings.ReplaceAll(compact, " ", "")
		for _, want := range []string{"124 change-text lines omitted", "2 files omitted", "Ctrl+O history, Ctrl+G open transcript", "Decision: Waiting for you"} {
			if !strings.Contains(compact, strings.ReplaceAll(want, " ", "")) {
				t.Fatalf("width %d missing %q: %s", width, want, view)
			}
		}
		if strings.Contains(view, "file-10") || strings.Contains(view, "tail") {
			t.Fatalf("unbounded preview: %s", view)
		}
	}
	full := Render(r, RenderContext{Key: RenderKey{Mode: RenderTranscript}})
	if !strings.Contains(full, `tail\x1b[2J`) || !strings.Contains(full, "scope") || strings.Count(full, "body") != 100 {
		t.Fatal("full evidence lost")
	}
}

func TestSmallFileApprovalHasNoOmission(t *testing.T) {
	p := &room.FileApproval{Summary: "File changes", Files: []room.ApprovalFile{{Path: "目录/whole\tpath\x1b", ChangeKind: "write", Format: "content", Text: "hello\n"}}}
	view := fileApprovalPreview(p, 120)
	if strings.Contains(view, "omitted") || strings.Contains(view, "Ctrl+") || !strings.Contains(view, "目录/whole\tpath\x1b") {
		t.Fatalf("unexpected preview: %q", view)
	}
}
