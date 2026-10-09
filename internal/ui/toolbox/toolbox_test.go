package toolbox

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/roomscript/coderoom/internal/participant"
)

func TestToolboxCells_orderByStatusThenAlias(t *testing.T) {
	now := time.Unix(100, 0)

	ps := []participant.View{
		{Alias: "bob", Status: participant.StatusIdle, Since: now},
		{Alias: "ada", Status: participant.StatusWorking, Since: now.Add(-5 * time.Second)},
		{Alias: "zoe", Status: participant.StatusWorking, Since: now.Add(-7 * time.Second)},
		{Alias: "cat", Status: participant.StatusStarting, Since: now.Add(-2 * time.Second)},
	}

	out := renderParticipantCells(cellWidth()*10, now, ps)
	// working first (alias-sorted within tier): ada, zoe; then starting: cat; then idle: bob
	wantOrder := []string{"ada", "zoe", "cat", "bob"}
	pos := -1
	for _, alias := range wantOrder {
		i := indexAfter(out, alias, pos)
		if i < 0 {
			t.Fatalf("expected %q in output; got: %q", alias, out)
		}
		pos = i
	}
}

func TestToolboxCells_overflowShowsPlusN(t *testing.T) {
	now := time.Unix(100, 0)

	ps := make([]participant.View, 0, 10)
	for i := range 10 {
		ps = append(ps, participant.View{
			Alias:  string(rune('a' + i)),
			Status: participant.StatusIdle,
			Since:  now,
		})
	}
	// Force only 2 cells wide -> 1 visible + overflow cell.
	out := renderParticipantCells(cellWidth()*2, now, ps)
	if !strings.Contains(out, "+9") {
		t.Fatalf("expected overflow +9, got: %q", out)
	}
}

func TestToolboxGlyphs_andElapsedFormatting(t *testing.T) {
	base := time.Unix(100, 0)

	ps := []participant.View{
		{Alias: "ada", Status: participant.StatusWorking, Since: base.Add(-10 * time.Second)},
	}
	out := renderParticipantCells(cellWidth()*4, base, ps)
	if !strings.Contains(out, "⏹") && !strings.Contains(out, "◆") {
		t.Fatalf("expected working glyph in output, got: %q", out)
	}
	if !strings.Contains(out, "(10s)") {
		t.Fatalf("expected 10s elapsed in output, got: %q", out)
	}

	ps = append(ps, participant.View{
		Alias:  "bob",
		Status: participant.StatusCrashed,
		Since:  base.Add(-3*time.Minute - 12*time.Second),
	})
	out = renderParticipantCells(cellWidth()*4, base, ps)
	if !strings.Contains(out, "✖") {
		t.Fatalf("expected crashed glyph in output, got: %q", out)
	}
	if !strings.Contains(out, "(3m12s)") {
		t.Fatalf("expected 3m12s elapsed in output, got: %q", out)
	}
}

func TestParticipantsWantsTick(t *testing.T) {
	now := time.Unix(100, 0)

	if New().WantsTick() {
		t.Fatal("expected false for empty participants")
	}
	idleOnly, _ := New().SetParticipants([]participant.View{{Alias: "ada", Status: participant.StatusIdle, Since: now}})
	if idleOnly.WantsTick() {
		t.Fatal("expected false for idle-only participants")
	}
	working, _ := New().SetParticipants([]participant.View{{Alias: "ada", Status: participant.StatusWorking, Since: now}})
	if !working.WantsTick() {
		t.Fatal("expected true for working participant")
	}
	keepalive, _ := New().SetParticipants([]participant.View{{Alias: "ada", Status: participant.StatusKeepalive, Since: now}})
	if !keepalive.WantsTick() {
		t.Fatal("expected true for keepalive participant")
	}
	starting, _ := New().SetParticipants([]participant.View{{Alias: "ada", Status: participant.StatusStarting, Since: now}})
	if !starting.WantsTick() {
		t.Fatal("expected true for starting participant")
	}
	crashed, _ := New().SetParticipants([]participant.View{{Alias: "ada", Status: participant.StatusCrashed, Since: now}})
	if !crashed.WantsTick() {
		t.Fatal("expected true for crashed participant")
	}
}

func TestCell_longAliasDoesNotTruncateElapsed(t *testing.T) {
	now := time.Unix(100, 0)
	// Alias longer than aliasMax; elapsed must still appear in full.
	ps := []participant.View{
		{Alias: "verylongaliasname", Status: participant.StatusWorking, Since: now.Add(-5 * time.Second)},
	}
	out := ansi.Strip(renderParticipantCells(cellWidth()*4, now, ps))
	if !strings.Contains(out, "(5s)") {
		t.Errorf("elapsed must not be truncated when alias is long; got %q", out)
	}
}

func TestCell_elapsedImmediatelyAfterAlias(t *testing.T) {
	now := time.Unix(100, 0)
	ps := []participant.View{
		{Alias: "ada", Status: participant.StatusWorking, Since: now.Add(-5 * time.Second)},
	}
	out := ansi.Strip(renderParticipantCells(cellWidth()*4, now, ps))
	// elapsed must appear right after alias with no padding gap between them
	if !strings.Contains(out, "ada (5s)") {
		t.Errorf("expected 'ada (5s)' without padding gap; got %q", out)
	}
}

func TestCell_colorPreservesContent(t *testing.T) {
	now := time.Unix(100, 0)
	plain := []participant.View{
		{Alias: "ada", Status: participant.StatusIdle, Since: now},
	}
	colored := []participant.View{
		{Alias: "ada", Status: participant.StatusIdle, Since: now, Color: "#ff0000"},
	}
	w := cellWidth() * 4
	// Stripped content must be identical whether a color is set or not.
	if ansi.Strip(renderParticipantCells(w, now, plain)) != ansi.Strip(renderParticipantCells(w, now, colored)) {
		t.Error("expected same visible content with and without color")
	}
}

func TestCell_colorUnsetAddsNoExtraEscapes(t *testing.T) {
	now := time.Unix(100, 0)
	w := cellWidth() * 4
	plain := renderParticipantCells(w, now, []participant.View{
		{Alias: "ada", Status: participant.StatusIdle, Since: now},
	})
	colored := renderParticipantCells(w, now, []participant.View{
		{Alias: "ada", Status: participant.StatusIdle, Since: now, Color: "#ff0000"},
	})
	// Color is the only permitted difference between the two outputs.
	// Stripping escape codes from both must yield the same string.
	if ansi.Strip(plain) != ansi.Strip(colored) {
		t.Errorf("expected stripped plain == stripped colored;\nplain:   %q\ncolored: %q", ansi.Strip(plain), ansi.Strip(colored))
	}
	// And plain must have no MORE escape codes than colored.
	if len(plain) > len(ansi.Strip(plain)) && len(colored) <= len(ansi.Strip(colored)) {
		t.Error("plain output has escape codes but colored output does not — unexpected")
	}
}

func indexAfter(s, sub string, after int) int {
	start := 0
	if after >= 0 {
		start = min(after+1, len(s))
	}
	j := strings.Index(s[start:], sub)
	if j < 0 {
		return -1
	}
	return start + j
}
