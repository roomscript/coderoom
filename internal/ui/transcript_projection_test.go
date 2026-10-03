package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/trigosec/coderoom/internal/agent"
	"github.com/trigosec/coderoom/internal/interpreter"
	"github.com/trigosec/coderoom/internal/participant"
	roomstate "github.com/trigosec/coderoom/internal/room"
	uiroom "github.com/trigosec/coderoom/internal/ui/room"
)

func TestTranscriptProjection_updatesCanonicalStreamAroundPresentationNotices(t *testing.T) {
	m := makeReadyModel(t)
	m.room = m.room.AppendSystem("startup tip")
	stream := roomstate.NewAgentRecord("ada", agent.Message{StreamID: "output", Mode: agent.ModeStream, Content: agent.Output{Text: "hello"}})
	delta := roomstate.Delta{Version: 1, RecordUpdates: []roomstate.IndexedRecord{{Index: 0, Record: stream}}, Meta: roomstate.DeltaMeta{OpenStreams: []roomstate.OpenStream{{StreamID: "output", RecordIdx: 0}}}}
	m, _ = m.handleInterpreterEvent(interpreter.TranscriptChanged{Delta: delta})
	m.room = m.room.AppendSystem("local help")
	stream = roomstate.NewAgentRecord("ada", agent.Message{StreamID: "output", Mode: agent.ModeStream, Content: agent.Output{Text: "hello world"}})
	delta.Version = 2
	delta.RecordUpdates[0].Record = stream
	m, _ = m.handleInterpreterEvent(interpreter.TranscriptChanged{Delta: delta})
	m, _ = m.handleInterpreterEvent(interpreter.TranscriptChanged{Delta: delta})
	records := m.room.HistoryRecords()
	if len(records) != 3 || records[0].Text != "startup tip" || records[1].Text != "hello world" || records[2].Text != "local help" {
		t.Fatalf("records = %#v", records)
	}
	if index, ok := m.room.StreamingIdx("ada"); !ok || index != 1 {
		t.Fatalf("stream index = %d, %v", index, ok)
	}
	delta.Version = 3
	delta.Meta.OpenStreams = nil
	delta.Meta.Departed = map[string]bool{"ada": true}
	delta.RecordUpdates = nil
	m, _ = m.handleInterpreterEvent(interpreter.TranscriptChanged{Delta: delta})
	if m.room.IsStreaming("ada") || !m.room.IsDeparted("ada") {
		t.Fatal("canonical stream/departure metadata was not presented")
	}
}

func TestTranscriptProjection_semanticEventsAndSnapshotsDoNotEchoRecords(t *testing.T) {
	m := makeReadyModel(t)
	records := []roomstate.Record{{Kind: roomstate.KindUserInput, Text: "/handoff ada turing"}, {Kind: roomstate.KindSystem, Text: "handoff audit"}}
	delta := roomstate.Delta{Version: 1, RecordUpdates: []roomstate.IndexedRecord{{Index: 0, Record: records[0]}, {Index: 1, Record: records[1]}}}
	m, _ = m.handleInterpreterEvent(interpreter.TranscriptChanged{Delta: delta})
	for _, event := range []interpreter.Event{
		interpreter.InputAccepted{Raw: "/handoff ada turing"},
		interpreter.StagedInputDispatched{Raw: "/handoff ada turing"},
		interpreter.HandoffCompleted{Preview: "handoff audit"},
		interpreter.StateChanged{Snapshot: interpreter.Snapshot{Room: roomstate.Snapshot{Version: 1, Records: records}}},
	} {
		m, _ = m.handleInterpreterEvent(event)
	}
	if got := m.room.HistoryRecords(); len(got) != 2 || got[0].Text != records[0].Text || got[1].Text != records[1].Text {
		t.Fatalf("records = %#v", got)
	}
}

func TestSnapshotColors_repaintsOnlyWhenMappingChanges(t *testing.T) {
	initial := []participant.View{{Alias: "ada", Color: "#ff0000"}, {Alias: "tim", Color: "#00ff00"}}
	tests := []struct {
		name         string
		participants []participant.View
		repaint      bool
	}{
		{name: "unchanged", participants: initial},
		{name: "reordered", participants: []participant.View{initial[1], initial[0]}},
		{name: "status only", participants: []participant.View{{Alias: "ada", Color: "#ff0000", Status: participant.StatusWorking, StartupReady: true}, initial[1]}},
		{name: "color changes", participants: []participant.View{{Alias: "ada", Color: "#0000ff"}, initial[1]}, repaint: true},
		{name: "alias added", participants: append(append([]participant.View(nil), initial...), participant.View{Alias: "cat", Color: "#ffffff"}), repaint: true},
		{name: "alias removed", participants: initial[:1], repaint: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := makeReadyModel(t)
			calls := 0
			m.room = uiroom.NewPresenter(func(alias string) string { calls++; return m.colors[alias] }, "#888888")
			m = m.handleResize(tea.WindowSizeMsg{Width: 80, Height: 24})
			m, _ = m.presentInterpreterSnapshot(interpreter.Snapshot{Participants: initial})
			record := roomstate.NewAgentRecord("ada", agent.Message{Mode: agent.ModeSingle, Content: agent.Output{Text: "cached output"}})
			m, _ = m.handleInterpreterEvent(interpreter.TranscriptChanged{Delta: roomstate.Delta{Version: 1, RecordUpdates: []roomstate.IndexedRecord{{Index: 0, Record: record}}}})
			calls = 0
			m, _ = m.presentInterpreterSnapshot(interpreter.Snapshot{Participants: tt.participants})
			if (calls > 0) != tt.repaint {
				t.Fatalf("color resolution calls = %d, want repaint = %v", calls, tt.repaint)
			}
			for _, view := range tt.participants {
				if m.colors[view.Alias] != view.Color {
					t.Fatalf("color for %s = %q", view.Alias, m.colors[view.Alias])
				}
			}
		})
	}
}
