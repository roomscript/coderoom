package record

import (
	"testing"

	"github.com/charmbracelet/x/ansi"
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
