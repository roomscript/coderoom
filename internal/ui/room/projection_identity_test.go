package room

import (
	"slices"
	"testing"

	"github.com/roomscript/coderoom/internal/agent"
	"github.com/roomscript/coderoom/internal/participant"
	roomstate "github.com/roomscript/coderoom/internal/room"
)

func TestPresenter_replacementKeepsCanonicalOwnersAroundLocalNotices(t *testing.T) {
	original := participant.New("ada", "", participant.InitiativeManual).Alias
	replacement := participant.New("ada", "", participant.InitiativeManual).Alias
	m := NewPresenter(nil, nil, "").HandleResize(80, 20)
	m = m.AppendSystem("before canonical history")
	m = m.ApplyTranscript(streamingIdentityDelta(1, original, 0, "original output"))
	m = m.AppendSystem("between canonical records")
	delta := streamingIdentityDelta(2, replacement, 1, "replacement output")
	delta.Meta.Departed = map[participant.Alias]bool{original: true}
	m = m.ApplyTranscript(delta)
	assertProjectedReplacement(t, m, original, replacement)

	m = m.ApplyTranscript(roomstate.Delta{Version: 3, Meta: delta.Meta, RecordUpdates: []roomstate.IndexedRecord{{Index: 0, Record: roomstate.NewAgentRecord(original, agent.Message{Mode: agent.ModeStream, StreamID: "output", Content: agent.Output{Text: "late original output"}})}}})
	assertProjectedReplacement(t, m, original, replacement)
	records := m.HistoryRecords()
	if records[1].Text != "late original output" || records[3].Text != "replacement output" {
		t.Fatal("late original output changed replacement record")
	}
	m = m.ApplyTranscript(delta)
	if len(m.HistoryRecords()) != len(records) {
		t.Fatal("stale transcript version duplicated records")
	}
}

func streamingIdentityDelta(version uint64, alias participant.Alias, index int, text string) roomstate.Delta {
	return roomstate.Delta{Version: version,
		RecordUpdates: []roomstate.IndexedRecord{{Index: index, Record: roomstate.NewAgentRecord(alias, agent.Message{Mode: agent.ModeStream, StreamID: "output", Content: agent.Output{Text: text}})}},
		Meta:          roomstate.DeltaMeta{OpenStreams: []roomstate.OpenStream{{Alias: alias, StreamID: "output", RecordIdx: index}}},
	}
}

func assertProjectedReplacement(t *testing.T, m Model, original, replacement participant.Alias) {
	t.Helper()
	if !m.IsDeparted(original) || m.IsDeparted(replacement) {
		t.Fatal("departure lost originating instance")
	}
	if m.IsStreaming(original) || !m.IsStreaming(replacement) {
		t.Fatal("stream lost originating instance")
	}
	if index, ok := m.StreamingIdx(replacement); !ok || index != 3 {
		t.Fatalf("projected replacement stream = %d, %v", index, ok)
	}
	records := m.HistoryRecords()
	if records[1].Alias != original || records[3].Alias != replacement {
		t.Fatal("canonical record ownership changed during projection")
	}
}

func TestPresenter_forwardsSeparateInstanceAndNameColorLookups(t *testing.T) {
	original := participant.New("ada", "", participant.InitiativeManual).Alias
	replacement := participant.New("ada", "", participant.InitiativeManual).Alias
	var owners []participant.Alias
	var names []string
	m := NewPresenter(func(alias participant.Alias) string { owners = append(owners, alias); return "2" }, func(name string) string { names = append(names, name); return "2" }, "8").HandleResize(80, 20)
	delta := streamingIdentityDelta(1, replacement, 0, "replacement output")
	delta.Meta.Departed = map[participant.Alias]bool{original: true}
	m = m.ApplyTranscript(delta).AppendUserInput("hello", []string{"ada"})
	owners, names = nil, nil
	m = m.RefreshColors()
	if !slices.Equal(owners, []participant.Alias{replacement}) || !slices.Equal(names, []string{"ada"}) {
		t.Fatalf("color lookups: owners %v, names %v", owners, names)
	}
	owners, names = nil, nil
	m.HistoryPlainText()
	if !slices.Equal(owners, []participant.Alias{replacement}) || !slices.Equal(names, []string{"ada"}) {
		t.Fatal("transcript color lookup changed instance ownership")
	}
}

func TestPresenter_snapshotQueriesDistinguishReasoningInstances(t *testing.T) {
	original := participant.New("ada", "", participant.InitiativeManual).Alias
	replacement := participant.New("ada", "", participant.InitiativeManual).Alias
	record := roomstate.NewAgentRecord(replacement, agent.Message{Mode: agent.ModeStream, StreamID: "reasoning", Content: agent.Reasoning{Text: "thinking"}})
	m := NewPresenter(nil, nil, "").HandleResize(80, 20)
	m = m.SetHistorySnapshot(roomstate.Snapshot{Records: []roomstate.Record{record}, Departed: map[participant.Alias]bool{original: true}, OpenStreams: []roomstate.OpenStream{{Alias: replacement, StreamID: "reasoning", RecordIdx: 0}}})
	if m.IsReasoningStreaming(original) || !m.IsReasoningStreaming(replacement) {
		t.Fatal("reasoning indicator transferred between instances")
	}
	if !m.IsDeparted(original) || m.IsDeparted(replacement) {
		t.Fatal("snapshot departure transferred to replacement")
	}
}
