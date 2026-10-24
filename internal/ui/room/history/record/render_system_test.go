package record

import (
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/roomscript/coderoom/internal/participant"
)

func TestRenderSystem_multilinePreservesWhitespace(t *testing.T) {
	withANSIProfile(t, func() {
		tests := []struct {
			name  string
			width int
			text  string
			want  string
		}{
			{"unequal lines", 80, "short\na much longer line", "short\na much longer line"},
			{"blank line", 80, "first line\n\nlast", "first line\n\nlast"},
			{"source spaces", 80, "  indented\ntrailing  \nx", "  indented\ntrailing  \nx"},
			{"wrapped", 14, "short\nwrapped system line", "short\nwrapped system\nline"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				r := Record{Kind: KindSystem, Text: tt.text}
				out := Render(r, RenderContext{Key: RenderKey{Mode: RenderViewport, Width: tt.width}})
				if got := ansi.Strip(out); got != tt.want {
					t.Fatalf("unexpected system layout:\nwant: %q\n got: %q", tt.want, got)
				}
			})
		}
	})
}

func TestRenderSystem_participantRecordUsesParticipantColor(t *testing.T) {
	withANSIProfile(t, func() {
		ada := participant.Alias{Name: "ada"}
		colors := func(alias participant.Alias) string {
			if alias != ada {
				return ""
			}
			return "6"
		}
		tests := []struct {
			name  string
			alias participant.Alias
			want  lipgloss.Style
		}{
			{"participant", ada, systemStyle.Foreground(lipgloss.Color("6"))},
			{"no participant", participant.Alias{}, systemStyle},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				text := "[ada joined · role: builder]"
				r := Record{Kind: KindSystem, Alias: tt.alias, Text: text}
				out := Render(r, RenderContext{Key: RenderKey{Mode: RenderViewport, Width: 80}, ColorForAlias: colors})
				if want := tt.want.Render(text); out != want {
					t.Fatalf("output = %q, want %q", out, want)
				}
			})
		}
	})
}
