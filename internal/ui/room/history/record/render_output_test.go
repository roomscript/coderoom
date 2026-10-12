package record

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
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
