package history

import (
	"testing"

	roomstate "github.com/roomscript/coderoom/internal/room"
	rec "github.com/roomscript/coderoom/internal/ui/room/history/record"
)

func systemRecords(n int) []rec.Record {
	records := make([]rec.Record, n)
	for i := range records {
		records[i] = rec.Record{Kind: rec.KindSystem, Text: "[x]"}
	}
	return records
}

func TestReplaceState_whenAtBottom_doesNotImplicitlyFollow(t *testing.T) {
	m := New(nil, "")
	m = m.SetSize(80, 10)
	records := systemRecords(25)
	m = m.ReplaceSnapshot(roomstate.Snapshot{Records: records})
	m = m.GotoBottom()
	if !m.AtBottom() {
		t.Fatal("expected viewport at bottom before new state")
	}
	beforeY := m.YOffset()

	records = append(records, rec.Record{Kind: rec.KindAgentOutput, Alias: "ada", Text: "hello"})
	m = m.ReplaceSnapshot(roomstate.Snapshot{Records: records})
	if m.YOffset() != beforeY {
		t.Fatalf("expected new state not to auto-follow when history layer is passive; before=%d after=%d", beforeY, m.YOffset())
	}
	if m.AtBottom() {
		t.Fatal("expected viewport not to remain at bottom without an explicit live policy")
	}
}

func TestReplaceState_whenScrolledUp_doesNotForceViewportToBottom(t *testing.T) {
	m := New(nil, "")
	m = m.SetSize(80, 10)
	records := systemRecords(25)
	m = m.ReplaceSnapshot(roomstate.Snapshot{Records: records})
	m = m.GotoBottom()
	m = m.ScrollUp(3)
	if m.AtBottom() {
		t.Fatal("expected viewport not at bottom after scrolling up")
	}
	y := m.YOffset()

	records = append(records, rec.Record{Kind: rec.KindAgentOutput, Alias: "ada", Text: "hello"})
	m = m.ReplaceSnapshot(roomstate.Snapshot{Records: records})
	if m.YOffset() != y {
		t.Fatalf("expected new state not to force viewport to bottom when scrolled up; yOffset changed from %d to %d", y, m.YOffset())
	}
}

func TestCursorLeftRight_crossesVisibleLineBoundaries(t *testing.T) {
	m := New(nil, "")
	m = m.SetSize(5, 4)
	m = m.ReplaceSnapshot(roomstate.Snapshot{
		Records: []rec.Record{{Kind: rec.KindSystem, Text: "abcde fghi"}},
	})
	m = m.GotoLiveEnd()

	if row, col := m.CursorPosition(); row == 0 && col == 0 {
		t.Fatalf("expected cursor at live end, got row=%d col=%d", row, col)
	}

	m = m.CursorLeft()
	m = m.CursorLeft()
	m = m.CursorLeft()
	m = m.CursorLeft()
	m = m.CursorLeft()
	m = m.CursorLeft()
	row, col := m.CursorPosition()
	if row != 0 || col != 5 {
		t.Fatalf("expected CursorLeft to cross into prior visible line; got row=%d col=%d", row, col)
	}

	m = m.CursorRight()
	row, col = m.CursorPosition()
	if row != 1 || col != 0 {
		t.Fatalf("expected CursorRight to cross into next visible line; got row=%d col=%d", row, col)
	}
}

func TestCursorUpDown_preservesPreferredColumn(t *testing.T) {
	m := New(nil, "")
	m = m.SetSize(6, 4)
	m = m.ReplaceSnapshot(roomstate.Snapshot{
		Records: []rec.Record{{Kind: rec.KindSystem, Text: "abcdef gh ij"}},
	})
	m = m.GotoLiveEnd()
	m = m.CursorLineStart()
	m = m.CursorRight()
	m = m.CursorRight()
	m = m.CursorRight()

	row, col := m.CursorPosition()
	if row == 0 {
		t.Fatalf("expected cursor to start on a later wrapped line, got row=%d col=%d", row, col)
	}

	m = m.CursorUp()
	upRow, upCol := m.CursorPosition()
	if upRow != row-1 || upCol != 3 {
		t.Fatalf("expected CursorUp to preserve preferred col=3; got row=%d col=%d", upRow, upCol)
	}

	m = m.CursorDown()
	downRow, downCol := m.CursorPosition()
	if downRow != row || downCol != 3 {
		t.Fatalf("expected CursorDown to restore preferred col=3; got row=%d col=%d", downRow, downCol)
	}
}

func TestApplyRoomDelta_whenBottomAlignedButCursorNotAtLiveEnd_preservesCursorAndViewport(t *testing.T) {
	m := New(nil, "")
	m = m.SetSize(8, 3)
	records := systemRecords(8)
	m = m.ReplaceSnapshot(roomstate.Snapshot{Records: records})
	m = m.GotoLiveEnd()
	m = m.CursorUp()
	m = m.CursorLineStart()

	beforeRow, beforeCol := m.CursorPosition()
	beforeY := m.YOffset()

	m = m.ApplyRoomDelta(roomstate.Delta{
		RecordUpdates: []roomstate.IndexedRecord{{
			Index:  len(records),
			Record: rec.Record{Kind: rec.KindSystem, Text: "[new]"},
		}},
		Meta: roomstate.DeltaMeta{Departed: map[string]bool{}},
	})

	afterRow, afterCol := m.CursorPosition()
	if afterRow != beforeRow || afterCol != beforeCol {
		t.Fatalf("expected cursor to remain stable away from live end; before=(%d,%d) after=(%d,%d)", beforeRow, beforeCol, afterRow, afterCol)
	}
	if m.YOffset() != beforeY {
		t.Fatalf("expected passive history delta not to move viewport; before=%d after=%d", beforeY, m.YOffset())
	}
}

func TestSetSize_whenCursorAtLiveEnd_reflowDoesNotImplicitlyRearmFollow(t *testing.T) {
	m := New(nil, "")
	m = m.SetSize(12, 4)
	m = m.ReplaceSnapshot(roomstate.Snapshot{
		Records: []rec.Record{{Kind: rec.KindSystem, Text: "abcdefghijklmno"}},
	})
	m = m.GotoLiveEnd()

	m = m.SetSize(6, 4)
	if !m.cursorAtLiveEnd() {
		t.Fatalf("expected width reflow to preserve live-end cursor; got row=%d col=%d", m.cursor.Row, m.cursor.Col)
	}

	m = m.ApplyRoomDelta(roomstate.Delta{
		RecordUpdates: []roomstate.IndexedRecord{{
			Index:  1,
			Record: rec.Record{Kind: rec.KindSystem, Text: "[new]"},
		}},
		Meta: roomstate.DeltaMeta{Departed: map[string]bool{}},
	})
	if m.cursorAtLiveEnd() {
		t.Fatalf("expected history delta not to re-arm live cursor on its own; got row=%d col=%d", m.cursor.Row, m.cursor.Col)
	}
}

func TestSetSize_whenCursorNotAtLiveEnd_reflowsBySurfaceCoordinate(t *testing.T) {
	m := New(nil, "")
	m = m.SetSize(20, 4)
	m = m.ReplaceSnapshot(roomstate.Snapshot{
		Records: []rec.Record{{Kind: rec.KindSystem, Text: "abcdefghijkl"}},
	})
	m = m.GotoLiveEnd()
	m = m.CursorLeft()
	m = m.CursorLeft()
	beforeCoord, ok := m.cursorSurfaceCoord()
	if !ok {
		t.Fatal("expected visible cursor before resize")
	}

	m = m.SetSize(6, 4)
	afterCoord, ok := m.cursorSurfaceCoord()
	if !ok {
		t.Fatal("expected visible cursor after resize")
	}
	if afterCoord != beforeCoord {
		t.Fatalf("expected width reflow to preserve surface coordinate; before=%+v after=%+v", beforeCoord, afterCoord)
	}
}

func TestSetSize_whenCursorOnSeparatorBlankLine_preservesSeparatorRow(t *testing.T) {
	m := New(nil, "")
	m = m.SetSize(20, 5)
	m = m.ReplaceSnapshot(roomstate.Snapshot{
		Records: []rec.Record{
			{Kind: rec.KindAgentOutput, Alias: "ada", Text: "first block"},
			{Kind: rec.KindAgentOutput, Alias: "bob", Text: "second block that wraps after resize"},
		},
	})
	m = m.GotoLiveEnd()
	for range 12 {
		row, _ := m.CursorPosition()
		if lineWidth(m.lines[row]) == 0 {
			break
		}
		m = m.CursorUp()
	}

	beforeRow, beforeCol := m.CursorPosition()
	if beforeCol != 0 {
		t.Fatalf("expected separator cursor column 0 before resize, got row=%d col=%d", beforeRow, beforeCol)
	}
	if lineWidth(m.lines[beforeRow]) != 0 {
		t.Fatalf("expected cursor to be on separator blank line before resize, got row=%d width=%d", beforeRow, lineWidth(m.lines[beforeRow]))
	}
	beforeCoord, ok := m.cursorSurfaceCoord()
	if !ok {
		t.Fatal("expected visible cursor before resize")
	}

	m = m.SetSize(10, 5)
	afterRow, afterCol := m.CursorPosition()
	if afterCol != 0 {
		t.Fatalf("expected separator cursor column 0 after resize, got row=%d col=%d", afterRow, afterCol)
	}
	if lineWidth(m.lines[afterRow]) != 0 {
		t.Fatalf("expected cursor to remain on separator blank line after resize, got row=%d width=%d", afterRow, lineWidth(m.lines[afterRow]))
	}
	afterCoord, ok := m.cursorSurfaceCoord()
	if !ok {
		t.Fatal("expected visible cursor after resize")
	}
	if afterCoord != beforeCoord {
		t.Fatalf("expected width reflow to preserve surface coordinate; before=%+v after=%+v", beforeCoord, afterCoord)
	}
}
