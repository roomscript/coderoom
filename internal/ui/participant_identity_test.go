package ui

import (
	"strings"
	"testing"

	"github.com/roomscript/coderoom/internal/agent"
	"github.com/roomscript/coderoom/internal/interpreter"
	"github.com/roomscript/coderoom/internal/participant"
	roomstate "github.com/roomscript/coderoom/internal/room"
	"github.com/roomscript/coderoom/internal/ui/palette"
	rec "github.com/roomscript/coderoom/internal/ui/room/history/record"
)

func TestPresenter_delayedOriginalOutputKeepsReplacementColorsAndStreams(t *testing.T) {
	original := participant.New("ada", "", participant.InitiativeManual).Alias
	replacement := participant.New("ada", "", participant.InitiativeManual).Alias
	m := makeReadyModel(t)
	m, _ = m.presentInterpreterSnapshot(interpreter.Snapshot{Participants: []participant.View{{Alias: replacement, Color: "2"}}})
	originalRecord := roomstate.NewAgentRecord(original, agent.Message{Mode: agent.ModeSingle, Content: agent.Output{Text: "original output"}})
	replacementRecord := roomstate.NewAgentRecord(replacement, agent.Message{Mode: agent.ModeStream, StreamID: "output", Content: agent.Output{Text: "replacement output"}})
	meta := roomstate.DeltaMeta{Departed: map[participant.Alias]bool{original: true}, OpenStreams: []roomstate.OpenStream{{Alias: replacement, StreamID: "output", RecordIdx: 1}}}
	m, _ = m.handleInterpreterEvent(interpreter.TranscriptChanged{Delta: roomstate.Delta{Version: 1, Meta: meta, RecordUpdates: []roomstate.IndexedRecord{{Index: 0, Record: originalRecord}, {Index: 1, Record: replacementRecord}}}})
	originalRecord = roomstate.NewAgentRecord(original, agent.Message{Mode: agent.ModeStream, StreamID: "output", Content: agent.Output{Text: "late original output"}})
	m, _ = m.handleInterpreterEvent(interpreter.TranscriptChanged{Delta: roomstate.Delta{Version: 2, Meta: meta, RecordUpdates: []roomstate.IndexedRecord{{Index: 0, Record: originalRecord}}}})
	if _, exists := m.colors[original]; exists {
		t.Fatal("original retained active color mapping")
	}
	if m.colors[replacement] != "2" {
		t.Fatal("replacement color changed after original output")
	}
	if m.room.IsStreaming(original) || !m.room.IsStreaming(replacement) {
		t.Fatal("delayed original output changed replacement stream")
	}
	records := m.room.HistoryRecords()
	if records[0].Alias != original || records[1].Alias != replacement {
		t.Fatal("historical record ownership changed")
	}
	assertRenderedIdentityRecord(t, m, originalRecord, palette.ColorDeparted)
	assertRenderedIdentityRecord(t, m, replacementRecord, "2")
}

func assertRenderedIdentityRecord(t *testing.T, m Model, record roomstate.Record, color string) {
	t.Helper()
	expected := rec.Render(record, rec.RenderContext{Key: rec.RenderKey{Mode: rec.RenderViewport, Width: m.room.Width()}, ColorForAlias: func(participant.Alias) string { return color }})
	if !strings.Contains(m.room.HistoryRenderedContent(), expected) {
		t.Fatalf("record for %v lost its color or output", record.Alias)
	}
}

func TestPresenter_routingNameColorFollowsCurrentSnapshot(t *testing.T) {
	original := participant.New("ada", "", participant.InitiativeManual).Alias
	replacement := participant.New("ada", "", participant.InitiativeManual).Alias
	m := makeReadyModel(t)
	m, _ = m.presentInterpreterSnapshot(interpreter.Snapshot{Participants: []participant.View{{Alias: original, Color: "1"}}})
	input := roomstate.Record{Kind: roomstate.KindUserInput, Text: "hello", Routing: []string{"ada"}}
	m, _ = m.handleInterpreterEvent(interpreter.TranscriptChanged{Delta: roomstate.Delta{Version: 1, RecordUpdates: []roomstate.IndexedRecord{{Index: 0, Record: input}}}})
	m, _ = m.presentInterpreterSnapshot(interpreter.Snapshot{Participants: []participant.View{{Alias: replacement, Color: "2"}}})
	expected := rec.Render(input, rec.RenderContext{Key: rec.RenderKey{Mode: rec.RenderViewport, Width: m.room.Width()}, ColorForName: func(string) string { return "2" }})
	if !strings.Contains(m.room.HistoryRenderedContent(), expected) {
		t.Fatal("routing label retained predecessor color")
	}
	if len(m.colors) != 1 || m.colors[replacement] != "2" {
		t.Fatal("display name lookup changed authoritative instance color map")
	}
}

func TestApprovalPresenter_displaysResolvedInstanceName(t *testing.T) {
	alias := participant.New("ada", "", participant.InitiativeManual).Alias
	m := makeReadyModel(t)
	m, _ = m.presentInterpreterSnapshot(interpreter.Snapshot{Approval: &interpreter.Approval{ID: 7, Alias: alias, Prompt: "approve work?", Options: []interpreter.ApprovalOption{{ID: "accept"}}}})
	if !strings.Contains(m.room.View(), "[→ ada] approve work?") {
		t.Fatalf("approval header lost participant name: %q", m.room.View())
	}
}
