package record

import (
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/roomscript/coderoom/internal/agent"
	"github.com/roomscript/coderoom/internal/participant"
	roomstate "github.com/roomscript/coderoom/internal/room"
)

func TestRender_colorLookupPreservesRecordOwner(t *testing.T) {
	original := participant.New("ada", "", participant.InitiativeManual).Alias
	replacement := participant.New("ada", "", participant.InitiativeManual).Alias
	contents := []struct {
		name    string
		content agent.MessageContent
	}{
		{"output", agent.Output{Text: "output"}},
		{"reasoning", agent.Reasoning{Text: "thinking"}},
		{"command", agent.Command{Command: "go test"}},
		{"file change", agent.FileChangeSet{}},
	}
	for _, content := range contents {
		t.Run(content.name, func(t *testing.T) {
			for _, mode := range []RenderMode{RenderViewport, RenderTranscript} {
				var lookedUp []participant.Alias
				ctx := RenderContext{Key: RenderKey{Mode: mode, Width: 80}, ColorForAlias: func(alias participant.Alias) string {
					lookedUp = append(lookedUp, alias)
					return ""
				}, ColorForName: func(string) string { t.Fatal("record owner resolved through name lookup"); return "" }}
				for _, alias := range []participant.Alias{original, replacement} {
					r := roomstate.NewAgentRecord(alias, agent.Message{Mode: agent.ModeSingle, Content: content.content})
					output := ansi.Strip(Render(r, ctx))
					if !strings.Contains(output, "ada") {
						t.Fatalf("missing display name: %q", output)
					}
				}
				if !slices.Equal(lookedUp, []participant.Alias{original, replacement}) {
					t.Fatalf("color owners = %v", lookedUp)
				}
			}
		})
	}
}

func TestRender_routingColorsUseDisplayNames(t *testing.T) {
	for _, mode := range []RenderMode{RenderViewport, RenderTranscript} {
		var lookedUp []string
		ctx := RenderContext{Key: RenderKey{Mode: mode, Width: 80}, ColorForName: func(name string) string {
			lookedUp = append(lookedUp, name)
			return ""
		}, ColorForAlias: func(participant.Alias) string { t.Fatal("routing name used as record owner"); return "" }}
		output := ansi.Strip(Render(Record{Kind: KindUserInput, Text: "hello", Routing: []string{"ada", "ben"}}, ctx))
		if !slices.Equal(lookedUp, []string{"ada", "ben"}) || !strings.Contains(output, "→ ada") || !strings.Contains(output, "→ ben") {
			t.Fatalf("routing = %q, color names = %v", output, lookedUp)
		}
	}
}
