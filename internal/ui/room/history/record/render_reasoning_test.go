package record

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
	"github.com/roomscript/coderoom/internal/agent"
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
			ColorForAlias: func(alias string) string {
				if alias != "alice" {
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
