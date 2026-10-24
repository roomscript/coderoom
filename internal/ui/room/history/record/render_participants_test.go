package record

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/roomscript/coderoom/internal/agent"
	"github.com/roomscript/coderoom/internal/participant"
)

const testResumeID = "0f8e2a64-5b1c-4d7e-9a3f-2c6b8d1e4f70"

func whoRecord() Record {
	return Record{Kind: KindSystem, Text: "[agents] ada (claude), tim (codex), zed", Participants: []participant.View{
		{
			Alias: participant.Alias{Name: "ada"}, Role: "builder", Status: participant.StatusWorking,
			Settings: agent.Settings{Backend: "claude", Model: "claude-opus-5-5", Effort: "medium"}, ResumeID: testResumeID,
		},
		{Alias: participant.Alias{Name: "tim"}, Status: participant.StatusIdle, Settings: agent.Settings{Backend: "codex"}, ResumeID: "thr_42"},
		{Alias: participant.Alias{Name: "zed"}, Role: "reviewer", Status: participant.StatusStarting},
	}}
}

func TestRenderParticipants_layout(t *testing.T) {
	table := strings.Join([]string{
		"   alias  role      backend  model            effort         resume-id",
		"◆  ada    builder   claude   claude-opus-5-5  medium         " + testResumeID,
		"●  tim    none      codex    codex-managed    codex-managed  thr_42",
		"◌  zed    reviewer  -        -                -              -",
	}, "\n")
	tests := []struct {
		name  string
		mode  RenderMode
		width int
		want  string
	}{
		{"wide viewport", RenderViewport, 120, table},
		{"transcript", RenderTranscript, 20, table},
		{"narrow viewport", RenderViewport, 40, strings.Join([]string{
			"◆ ada",
			"  role: builder",
			"  backend: claude",
			"  model: claude-opus-5-5",
			"  effort: medium",
			"  resume-id: " + testResumeID[:24],
			"  " + testResumeID[24:],
			"● tim",
			"  role: none",
			"  backend: codex",
			"  model: codex-managed",
			"  effort: codex-managed",
			"  resume-id: thr_42",
			"◌ zed",
			"  role: reviewer",
			"  backend: -",
			"  model: -",
			"  effort: -",
			"  resume-id: -",
		}, "\n")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := Render(whoRecord(), RenderContext{Key: RenderKey{Mode: tt.mode, Width: tt.width}})
			if got := ansi.Strip(out); got != tt.want {
				t.Fatalf("layout:\nwant:\n%s\n got:\n%s", tt.want, got)
			}
		})
	}
}

func TestRenderParticipants_resizeKeepsFullResumeID(t *testing.T) {
	for _, width := range []int{20, 40, 80, 120} {
		out := ansi.Strip(Render(whoRecord(), RenderContext{Key: RenderKey{Mode: RenderViewport, Width: width}}))
		if !strings.Contains(strings.NewReplacer("\n", "", " ", "").Replace(out), testResumeID) {
			t.Fatalf("width %d lost the resume ID:\n%s", width, out)
		}
		for _, line := range strings.Split(out, "\n") {
			if ansi.StringWidth(line) > width {
				t.Fatalf("width %d: line %q overflows", width, line)
			}
		}
	}
}

func TestRenderParticipants_rowsUseParticipantColors(t *testing.T) {
	withANSIProfile(t, func() {
		colors := func(alias participant.Alias) string {
			if alias.Name == "ada" {
				return "6"
			}
			return ""
		}
		for _, width := range []int{120, 40} {
			out := Render(whoRecord(), RenderContext{Key: RenderKey{Mode: RenderViewport, Width: width}, ColorForAlias: colors})
			ada := systemRecordStyle("6").Render(firstLineContaining(ansi.Strip(out), "ada"))
			tim := systemStyle.Render(firstLineContaining(ansi.Strip(out), "tim"))
			if !strings.Contains(out, ada) || !strings.Contains(out, tim) {
				t.Fatalf("width %d: rows not in participant colors:\n%q", width, out)
			}
		}
	})
}

func TestRenderParticipants_emptySnapshotRendersText(t *testing.T) {
	out := Render(Record{Kind: KindSystem, Text: "[no agents]"}, RenderContext{Key: RenderKey{Mode: RenderViewport, Width: 80}})
	if got := ansi.Strip(out); got != "[no agents]" {
		t.Fatalf("output = %q, want [no agents]", got)
	}
}

func firstLineContaining(text, substr string) string {
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, substr) {
			return line
		}
	}
	return ""
}
