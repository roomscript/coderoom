package record

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestRenderLogBody_prefixesEveryLine(t *testing.T) {
	r := Record{Kind: KindLog, Text: "a\nb\nc"}
	out := ansi.Strip(Render(r, RenderContext{Key: RenderKey{Mode: RenderViewport, Width: 80}}))
	lines := strings.Split(out, "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d: %#v", len(lines), lines)
	}
	for i, line := range lines {
		if !strings.HasPrefix(line, "▸ ") {
			t.Fatalf("expected line %d to start with %q, got %q", i, "▸ ", line)
		}
	}
}

func TestRenderLogBody_ignoresTrailingNewline(t *testing.T) {
	r := Record{Kind: KindLog, Text: "a\n"}
	out := ansi.Strip(Render(r, RenderContext{Key: RenderKey{Mode: RenderViewport, Width: 80}}))
	if strings.Contains(out, "\n▸ ") {
		t.Fatalf("expected trailing newline not to produce an extra %q line; got %q", "▸ ", out)
	}
}

func TestRenderLogBody_multilinePreservesWhitespace(t *testing.T) {
	withANSIProfile(t, func() {
		tests := []struct {
			name  string
			width int
			text  string
			want  string
		}{
			{"unequal lines", 80, "short\na much longer line\n", "▸ short\n▸ a much longer line"},
			{"blank line", 80, "first line\n\nlast", "▸ first line\n▸ \n▸ last"},
			{"source spaces", 80, "  indented\ntrailing  \nx", "▸   indented\n▸ trailing  \n▸ x"},
			{"wrapped", 16, "short\nwrapped log line here", "▸ short\n▸ wrapped log\n  line here"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				r := Record{Kind: KindLog, Text: tt.text}
				out := Render(r, RenderContext{Key: RenderKey{Mode: RenderViewport, Width: tt.width}})
				if got := ansi.Strip(out); got != tt.want {
					t.Fatalf("unexpected log layout:\nwant: %q\n got: %q", tt.want, got)
				}
			})
		}
	})
}
