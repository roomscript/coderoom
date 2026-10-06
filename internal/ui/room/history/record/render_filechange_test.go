package record

import (
	"regexp"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/roomscript/coderoom/internal/agent"
)

func TestRenderFileChangePending(t *testing.T) {
	colors := func(string) string { return "" }

	r := Record{Kind: KindFileChange, Alias: "agent"}
	got := renderFileChangeStripped(t, r, colors)
	if !strings.Contains(got, "● agent:") {
		t.Fatalf("expected participant header, got %q", got)
	}
	if !strings.Contains(got, "✎ …") {
		t.Fatalf("expected pending placeholder, got %q", got)
	}
}

func TestRenderFileChangeRendersListPreviewAndStatus(t *testing.T) {
	colors := func(string) string { return "" }

	fc := agent.Message{
		StreamID: "s1",
		Mode:     agent.ModeStream,
		Content: agent.FileChangeSet{
			Changes: []agent.FileChange{
				{Path: "a.txt", ChangeKind: "update", Diff: "l1\nl2\nl3\nl4\nl5\nl6\nl7\nl8\nl9\n"},
				{Path: "b.txt"},
			},
			Status: agent.ToolStatusCompleted,
		},
	}
	r := NewAgent("agent", fc)
	got := renderFileChangeStripped(t, r, colors)

	for _, needle := range []string{
		"● agent:",
		"✎ files:",
		"  - update a.txt",
		"  - b.txt",
		"  === a.txt (update)",
		"Ctrl+O history, Ctrl+G open transcript",
		"  " + string(agent.ToolStatusCompleted),
	} {
		if !strings.Contains(got, needle) {
			t.Fatalf("expected output to contain %q, got:\n%s", needle, got)
		}
	}
}

func TestRenderFileChangeDeduplicatesEntries(t *testing.T) {
	colors := func(string) string { return "" }

	fc := agent.Message{
		StreamID: "s1",
		Mode:     agent.ModeStream,
		Content: agent.FileChangeSet{
			Changes: []agent.FileChange{
				{Path: "a.txt", ChangeKind: "add"},
				{Path: "a.txt", ChangeKind: "add"},
				{Path: "a.txt", ChangeKind: "update"},
				{Path: "b.txt"},
				{Path: "b.txt"},
			},
			Status: agent.ToolStatusCompleted,
		},
	}
	r := NewAgent("agent", fc)
	got := renderFileChangeStripped(t, r, colors)

	if c := strings.Count(got, "- add a.txt"); c != 1 {
		t.Fatalf("expected one entry for '- add a.txt', got %d:\n%s", c, got)
	}
	if c := strings.Count(got, "- update a.txt"); c != 1 {
		t.Fatalf("expected one entry for '- update a.txt', got %d:\n%s", c, got)
	}
	if c := strings.Count(got, "- b.txt"); c != 1 {
		t.Fatalf("expected one entry for '- b.txt', got %d:\n%s", c, got)
	}
}

func TestRenderFileChangePencilPromptIsColorized(t *testing.T) {
	withANSIProfile(t, func() {
		colors := func(string) string { return "1" }
		fc := agent.Message{
			StreamID: "s1",
			Mode:     agent.ModeStream,
			Content: agent.FileChangeSet{
				Changes: []agent.FileChange{{Path: "a.txt"}},
			},
		}
		r := NewAgent("agent", fc)
		got := Render(r, RenderContext{Key: RenderKey{Mode: RenderViewport, Width: 80}, ColorForAlias: colors})

		re := regexp.MustCompile(`\x1b\[[0-9;]*m✎\s*\x1b\[[0-9;]*m`)
		if !re.MatchString(got) {
			t.Fatalf("expected colorized pencil prompt, got:\n%s", got)
		}
	})
}

func TestRenderFileChangeDiffIsColorized(t *testing.T) {
	withANSIProfile(t, func() {
		colors := func(string) string { return "" }
		fc := agent.Message{
			StreamID: "s1",
			Mode:     agent.ModeStream,
			Content: agent.FileChangeSet{
				Changes: []agent.FileChange{
					{
						Path:       "a.txt",
						ChangeKind: "update",
						Diff: "diff --git a/a.txt b/a.txt\n" +
							"index 0000000..1111111 100644\n" +
							"--- a/a.txt\n" +
							"+++ b/a.txt\n" +
							"@@ -1 +1 @@\n" +
							"-old\n" +
							"+new\n",
					},
				},
			},
		}
		r := NewAgent("agent", fc)
		got := Render(r, RenderContext{Key: RenderKey{Mode: RenderTranscript, Width: 0}, ColorForAlias: colors})

		wantLine := func(prefix string) {
			t.Helper()
			if !strings.Contains(ansi.Strip(got), prefix) {
				t.Fatalf("expected stripped output to contain %q, got:\n%s", prefix, ansi.Strip(got))
			}
			re := regexp.MustCompile(`(?m)^\s*\x1b\[[0-9;]*m.*` + regexp.QuoteMeta(prefix))
			if !re.MatchString(got) {
				t.Fatalf("expected line %q to be colorized, got:\n%s", prefix, got)
			}
		}

		wantLine("diff --git a/a.txt b/a.txt")
		wantLine("@@ -1 +1 @@")
		wantLine("-old")
		wantLine("+new")
	})
}

func renderFileChangeStripped(t *testing.T, r Record, colors func(string) string) string {
	t.Helper()
	return ansi.Strip(Render(r, RenderContext{Key: RenderKey{Mode: RenderViewport, Width: 80}, ColorForAlias: colors}))
}
