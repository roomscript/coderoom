package history

import (
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/roomscript/coderoom/internal/agent"
	"github.com/roomscript/coderoom/internal/participant"
	roomstate "github.com/roomscript/coderoom/internal/room"
	rec "github.com/roomscript/coderoom/internal/ui/room/history/record"
)

func TestHistory_streamIDsRemainScopedToParticipant(t *testing.T) {
	ada, ben := testAlias("ada"), participant.New("ben", "", participant.InitiativeManual).Alias
	output := agent.Message{Mode: agent.ModeStream, StreamID: "output", Content: agent.Output{Text: "work"}}
	reasoning := agent.Message{Mode: agent.ModeStream, StreamID: "reasoning", Content: agent.Reasoning{Text: "thinking"}}
	m := New(nil, nil, "").SetSize(80, 10)
	m = m.ReplaceSnapshot(roomstate.Snapshot{
		Records:     []rec.Record{roomstate.NewAgentRecord(ada, output), roomstate.NewAgentRecord(ben, output), roomstate.NewAgentRecord(ada, reasoning)},
		OpenStreams: []roomstate.OpenStream{{Alias: ada, StreamID: "output", RecordIdx: 0}, {Alias: ben, StreamID: "output", RecordIdx: 1}, {Alias: ada, StreamID: "reasoning", RecordIdx: 2}},
	})
	for index, alias := range []participant.Alias{ada, ben} {
		assertOutputStream(t, m, alias, index, true)
	}
	if !m.IsReasoningStreaming(ada) || m.IsReasoningStreaming(ben) {
		t.Fatal("reasoning stream lost its owner")
	}
	m = m.ApplyRoomDelta(roomstate.Delta{Meta: roomstate.DeltaMeta{OpenStreams: []roomstate.OpenStream{{Alias: ben, StreamID: "output", RecordIdx: 1}}}})
	if m.IsStreaming(ada) || m.IsReasoningStreaming(ada) || !m.IsStreaming(ben) {
		t.Fatal("stream cleanup affected other participant")
	}
	if _, ok := m.StreamingIdx(ada); ok {
		t.Fatal("closed output stream retained an index")
	}
}

func TestHistory_replacementDeltaKeepsDepartureAndStreamOwnership(t *testing.T) {
	original := participant.New("ada", "", participant.InitiativeManual).Alias
	replacement := participant.New("ada", "", participant.InitiativeManual).Alias
	output := agent.Message{Mode: agent.ModeStream, StreamID: "output", Content: agent.Output{Text: "work"}}
	var lookedUp []participant.Alias
	m := New(func(alias participant.Alias) string { lookedUp = append(lookedUp, alias); return "2" }, nil, "8").SetSize(80, 10)
	m = m.ReplaceSnapshot(roomstate.Snapshot{Records: []rec.Record{roomstate.NewAgentRecord(original, output)}, OpenStreams: []roomstate.OpenStream{{Alias: original, StreamID: "output", RecordIdx: 0}}})
	before := m.RenderedContent()
	lookedUp = nil
	m = m.ApplyRoomDelta(roomstate.Delta{
		RecordUpdates: []roomstate.IndexedRecord{{Index: 1, Record: roomstate.NewAgentRecord(replacement, output)}},
		Meta:          roomstate.DeltaMeta{Departed: map[participant.Alias]bool{original: true}, OpenStreams: []roomstate.OpenStream{{Alias: replacement, StreamID: "output", RecordIdx: 1}}},
	})
	assertReplacementOwnership(t, m, original, replacement)
	if m.resolveColor(original) != "8" || m.resolveColor(replacement) != "2" {
		t.Fatal("replacement inherited departed color")
	}
	if !slices.Equal(lookedUp, []participant.Alias{replacement, replacement}) {
		t.Fatalf("active color lookup owners = %v", lookedUp)
	}
	if m.RenderedContent() == before || !strings.Contains(m.PlainText(), "ada") {
		t.Fatal("history lost replacement rendering")
	}
	records := m.Records()
	expectedOld := rec.Render(records[0], rec.RenderContext{Key: rec.RenderKey{Mode: rec.RenderViewport, Width: 80}, ColorForAlias: func(participant.Alias) string { return "8" }})
	if !strings.Contains(m.RenderedContent(), expectedOld) {
		t.Fatal("departure did not invalidate original record color cache")
	}

}

func TestHistory_snapshotColorsSeparateInstancesAndRoutingNames(t *testing.T) {
	original := participant.New("ada", "", participant.InitiativeManual).Alias
	replacement := participant.New("ada", "", participant.InitiativeManual).Alias
	var owners []participant.Alias
	var names []string
	m := New(func(alias participant.Alias) string { owners = append(owners, alias); return "2" }, func(name string) string { names = append(names, name); return "2" }, "8").SetSize(80, 10)
	output := agent.Message{Mode: agent.ModeSingle, Content: agent.Output{Text: "work"}}
	m = m.ReplaceSnapshot(roomstate.Snapshot{
		Records:  []rec.Record{{Kind: rec.KindUserInput, Text: "hello", Routing: []string{"ada"}}, roomstate.NewAgentRecord(original, output), roomstate.NewAgentRecord(replacement, output)},
		Departed: map[participant.Alias]bool{original: true},
	})
	if !slices.Equal(owners, []participant.Alias{replacement}) || !slices.Equal(names, []string{"ada"}) {
		t.Fatalf("color lookups: owners %v, names %v", owners, names)
	}
	owners, names = nil, nil
	if text := ansi.Strip(m.PlainText()); !strings.Contains(text, "→ ada") {
		t.Fatalf("routing footer missing from transcript: %q", text)
	}
	if !slices.Equal(owners, []participant.Alias{replacement}) || !slices.Equal(names, []string{"ada"}) {
		t.Fatal("transcript color lookup lost ownership")
	}
}

func assertOutputStream(t *testing.T, m Model, alias participant.Alias, index int, exists bool) {
	t.Helper()
	if got, ok := m.StreamingIdx(alias); ok != exists || got != index {
		t.Fatalf("output stream for %v = %d, %v; want %d, %v", alias, got, ok, index, exists)
	}
	if m.IsStreaming(alias) != exists {
		t.Fatalf("streaming state for %v = %v, want %v", alias, m.IsStreaming(alias), exists)
	}
}

func assertReplacementOwnership(t *testing.T, m Model, original, replacement participant.Alias) {
	t.Helper()
	if !m.IsDeparted(original) {
		t.Fatal("original departure missing")
	}
	if m.IsDeparted(replacement) {
		t.Fatal("departure transferred to replacement")
	}
	assertOutputStream(t, m, original, 0, false)
	assertOutputStream(t, m, replacement, 1, true)
	records := m.Records()
	if records[0].Alias != original {
		t.Fatal("historical record ownership changed")
	}
	if records[1].Alias != replacement {
		t.Fatal("replacement record ownership changed")
	}
}
