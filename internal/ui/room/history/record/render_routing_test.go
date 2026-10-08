package record

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestRenderUserInput_distinguishesActualRoutingOutcomes(t *testing.T) {
	record := Record{Kind: KindUserInput, Text: "work", Routing: []string{"ada"}, FailedRouting: []string{"turing"}, UnsentRouting: []string{"grace"}}
	rendered := ansi.Strip(renderUserInput(record, 80, func(string) string { return "" }))
	for _, expected := range []string{"→ ada", "failed: turing", "not sent: grace"} {
		if !strings.Contains(rendered, expected) {
			t.Fatalf("missing %q in %q", expected, rendered)
		}
	}
	for _, absent := range []string{"→ turing", "→ grace"} {
		if strings.Contains(rendered, absent) {
			t.Fatalf("false delivery %q in %q", absent, rendered)
		}
	}
}
