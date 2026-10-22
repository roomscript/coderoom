package record

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/roomscript/coderoom/internal/agent"
	"github.com/roomscript/coderoom/internal/participant"
)

func TestRenderAgentOutput_marksHandoffSource(t *testing.T) {
	r := Record{
		Kind:          KindAgentOutput,
		Alias:         participant.New("ada", "", participant.InitiativeManual).Alias,
		Text:          "done",
		HandoffSource: true,
	}
	out := ansi.Strip(Render(r, RenderContext{Key: RenderKey{Mode: RenderViewport, Width: 80}}))
	if !strings.HasPrefix(out, "↦ ada:") {
		t.Fatalf("expected handoff source marker in header, got %q", out)
	}
}

func TestTranscript_preservesMultilineBodyIndentation(t *testing.T) {
	for _, kind := range []Kind{KindAgentOutput, KindReasoning} {
		for _, color := range []string{"", "2"} {
			t.Run(fmt.Sprintf("kind=%d/color=%s", kind, color), func(t *testing.T) {
				r := Record{Kind: kind, Alias: participant.New("ada", "", participant.InitiativeManual).Alias,
					Text: "first\n\n  nested\nlast\n", Msg: &agent.Message{}}
				out := ansi.Strip(Render(r, RenderContext{
					Key:           RenderKey{Mode: RenderTranscript},
					ColorForAlias: func(participant.Alias) string { return color },
				}))
				_, body, ok := strings.Cut(out, "\n\n")
				if !ok || body != "  first\n  \n    nested\n  last\n  " {
					t.Fatalf("unexpected transcript body: %q", out)
				}
			})
		}
	}
}
