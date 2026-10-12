package history

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/roomscript/coderoom/internal/participant"
	roomstate "github.com/roomscript/coderoom/internal/room"
	rec "github.com/roomscript/coderoom/internal/ui/room/history/record"
)

func TestResolveColor_activeReturnsFromLookup(t *testing.T) {
	m := New(func(alias participant.Alias) string {
		if alias == testAlias("ada") {
			return "#ff0000"
		}
		return ""
	}, nil, "#6b7280")
	if got := m.resolveColor(testAlias("ada")); got != "#ff0000" {
		t.Errorf("want #ff0000, got %q", got)
	}
}

func TestResolveColor_departedReturnsConfiguredColor(t *testing.T) {
	const grey = "#6b7280"
	m := New(nil, nil, grey)
	m.departed = map[participant.Alias]bool{testAlias("ada"): true}
	if got := m.resolveColor(testAlias("ada")); got != grey {
		t.Errorf("want %q, got %q", grey, got)
	}
}

func TestResolveColor_departedTakesPrecedenceOverActive(t *testing.T) {
	const grey = "#6b7280"
	m := New(func(participant.Alias) string { return "#ff0000" }, nil, grey)
	m.departed = map[participant.Alias]bool{testAlias("ada"): true}
	if got := m.resolveColor(testAlias("ada")); got != grey {
		t.Errorf("want departed color %q, got %q", grey, got)
	}
}

func TestJoinRenderedForViewport_insertsBlankLineBetweenNonSystemRecords(t *testing.T) {
	out := joinRenderedForViewport([]viewRecord{
		{record: rec.Record{Kind: rec.KindUserInput}},
		{record: rec.Record{Kind: rec.KindAgentOutput}},
	}, []string{"a", "b"})
	if strings.Count(out, "\n") != 2 {
		t.Fatalf("expected 2 newlines between non-system records, got %d (%q)", strings.Count(out, "\n"), out)
	}
	if ansi.Strip(out) != "a\n\nb" {
		t.Fatalf("unexpected joined output: %q", ansi.Strip(out))
	}
}

func TestJoinRenderedForViewport_systemRecordsStayCompact(t *testing.T) {
	out := joinRenderedForViewport([]viewRecord{
		{record: rec.Record{Kind: rec.KindUserInput}},
		{record: rec.Record{Kind: rec.KindSystem}},
		{record: rec.Record{Kind: rec.KindSystem}},
		{record: rec.Record{Kind: rec.KindAgentOutput}},
	}, []string{"u", "s1", "s2", "a"})
	if ansi.Strip(out) != "u\ns1\ns2\n\na" {
		t.Fatalf("unexpected joined output: %q", ansi.Strip(out))
	}
}

func TestApplyDelta_preservesUnchangedRenderCaches(t *testing.T) {
	calls := 0
	m := New(func(alias participant.Alias) string {
		calls++
		if alias == testAlias("ada") {
			return "#ff0000"
		}
		return "#00ff00"
	}, nil, "#6b7280")
	m = m.SetSize(80, 10)
	m = m.ReplaceSnapshot(roomstate.Snapshot{Records: []rec.Record{
		{Kind: rec.KindAgentOutput, Alias: testAlias("ada"), Text: "first"},
		{Kind: rec.KindAgentOutput, Alias: testAlias("bob"), Text: "second"},
	}})
	if calls == 0 {
		t.Fatal("expected initial render to resolve colors")
	}

	calls = 0
	m.ApplyRoomDelta(roomstate.Delta{
		RecordUpdates: []roomstate.IndexedRecord{{
			Index:  1,
			Record: rec.Record{Kind: rec.KindAgentOutput, Alias: testAlias("bob"), Text: "updated"},
		}},
		Meta: roomstate.DeltaMeta{
			Departed:    map[participant.Alias]bool{},
			OpenStreams: nil,
		},
	})
	if calls != 1 {
		t.Fatalf("expected exactly 1 rerendered record, got %d", calls)
	}
}

func TestApplyDelta_departedChangeInvalidatesRenderCaches(t *testing.T) {
	calls := 0
	m := New(func(alias participant.Alias) string {
		calls++
		if alias == testAlias("ada") {
			return "#ff0000"
		}
		return "#00ff00"
	}, nil, "#6b7280")
	m = m.SetSize(80, 10)
	m = m.ReplaceSnapshot(roomstate.Snapshot{
		Records: []rec.Record{
			{Kind: rec.KindAgentOutput, Alias: testAlias("ada"), Text: "first"},
			{Kind: rec.KindAgentOutput, Alias: testAlias("bob"), Text: "second"},
		},
		Departed: map[participant.Alias]bool{},
	})
	if calls == 0 {
		t.Fatal("expected initial render to resolve colors")
	}
	before := m.RenderedContent()

	calls = 0
	m = m.ApplyRoomDelta(roomstate.Delta{
		Meta: roomstate.DeltaMeta{
			Departed: map[participant.Alias]bool{testAlias("ada"): true},
		},
	})
	after := m.RenderedContent()
	if before == after {
		t.Fatal("expected departed change to alter rendered content")
	}
	if calls != 1 {
		t.Fatalf("expected active-color lookup for unchanged active records, got %d", calls)
	}
	if !m.IsDeparted(testAlias("ada")) {
		t.Fatal("expected ada to be marked departed")
	}
	if m.colorVersion == 0 {
		t.Fatal("expected departed change to bump colorVersion")
	}
}
