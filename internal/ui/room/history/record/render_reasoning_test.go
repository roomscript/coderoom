package record

import (
	"fmt"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
	"github.com/roomscript/coderoom/internal/agent"
	"github.com/roomscript/coderoom/internal/participant"
)

func withANSIProfile(t *testing.T, fn func()) {
	t.Helper()
	prev := lipgloss.Writer.Profile
	lipgloss.Writer.Profile = colorprofile.ANSI256
	t.Cleanup(func() { lipgloss.Writer.Profile = prev })
	fn()
}

func TestRenderReasoning_bodyUsesSystemStyleExceptEmphasis(t *testing.T) {
	withANSIProfile(t, func() {
		span := lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
		out := Render(NewAgent("alice", agent.Message{
			StreamID: "s1",
			Mode:     agent.ModeStream,
			Content:  agent.Reasoning{Text: "plain **bold** plain"},
		}), RenderContext{
			Key: RenderKey{
				Mode:  RenderViewport,
				Width: 200,
			},
			ColorForAlias: func(alias participant.Alias) string {
				if alias.Name != "alice" {
					return ""
				}
				return "6"
			},
		})

		if got, want := ansi.Strip(out), "◈ alice (thinking)\n\n  plain bold plain"; got != want {
			t.Fatalf("unexpected stripped output:\nwant: %q\n got: %q", want, got)
		}

		if !strings.Contains(out, span.Bold(true).Render("bold")) {
			t.Fatalf("expected participant color to apply to bold span; got %q", out)
		}
		if strings.Contains(out, span.Render("plain")) {
			t.Fatalf("expected participant color to not apply to plain text; got %q", out)
		}
	})
}

func TestRenderReasoning_multilineContractionsPreserveSpacing(t *testing.T) {
	withANSIProfile(t, func() {
		tests := []struct {
			width int
			want  string
		}{
			{20, "  I'm checking the\n  existing tests.\n  \n  I'll add coverage."},
			{40, "  I'm checking the existing tests.\n  \n  I'll add coverage."},
			{80, "  I'm checking the existing tests.\n  \n  I'll add coverage."},
			{200, "  I'm checking the existing tests.\n  \n  I'll add coverage."},
		}
		for _, color := range []string{"", "6"} {
			for _, tt := range tests {
				t.Run(fmt.Sprintf("color=%q/width=%d", color, tt.width), func(t *testing.T) {
					r := NewAgent("alice", agent.Message{
						StreamID: "s1",
						Mode:     agent.ModeStream,
						Content:  agent.Reasoning{Text: "I'm checking the existing tests.\n\nI'll add coverage."},
					})
					out := Render(r, RenderContext{
						Key:           RenderKey{Mode: RenderViewport, Width: tt.width},
						ColorForAlias: func(participant.Alias) string { return color },
					})
					want := "◈ alice (thinking)\n\n" + tt.want
					if got := ansi.Strip(out); got != want {
						t.Fatalf("unexpected reasoning layout:\nwant: %q\n got: %q", want, got)
					}
				})
			}
		}
	})
}
