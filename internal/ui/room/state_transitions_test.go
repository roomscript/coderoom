package room

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestStateTransitions_composeLiveToHistoryLive(t *testing.T) {
	m := seededRoomModel(t)

	if !strings.Contains(roomHeader(m), "LIVE") {
		t.Fatalf("expected seeded room to start live, got %q", roomHeader(m))
	}

	next, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: 'o', Mod: tea.ModCtrl}))
	if !strings.Contains(roomHeader(next), "LIVE") {
		t.Fatalf("expected Ctrl+O from compose live to enter history live, got %q", roomHeader(next))
	}
	if !historyCursorRendered(next) {
		t.Fatal("expected history live to render a visible cursor")
	}
	if !next.AtBottom() {
		t.Fatalf("expected history live to stay bottom-aligned; yOffset=%d", next.YOffset())
	}
	if !next.history.CursorAtLiveEnd() {
		row, col := next.HistoryCursorPosition()
		t.Fatalf("expected history live cursor at live end; row=%d col=%d", row, col)
	}
}

func TestStateTransitions_historyLiveToComposeLive_preservesFollow(t *testing.T) {
	m := seededRoomModel(t)
	m, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 'o', Mod: tea.ModCtrl}))

	next, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc}))
	if !strings.Contains(roomHeader(next), "LIVE") {
		t.Fatalf("expected Esc from history live to return to compose live, got %q", roomHeader(next))
	}
	if historyCursorRendered(next) {
		t.Fatal("expected compose live not to render the history cursor")
	}

	beforeY := next.YOffset()
	next = next.AppendSystem("[new]")
	if !next.AtBottom() {
		t.Fatalf("expected compose live to follow new output; yOffset=%d", next.YOffset())
	}
	if next.YOffset() < beforeY {
		t.Fatalf("expected new output not to move viewport upward; before=%d after=%d", beforeY, next.YOffset())
	}
}

func TestStateTransitions_historyBrowseAtBottomToComposeLive(t *testing.T) {
	for _, key := range []struct {
		name string
		code rune
	}{
		{"previous row", tea.KeyUp},
		{"line start", tea.KeyHome},
	} {
		t.Run(key.name, func(t *testing.T) {
			m := seededRoomModel(t)
			m, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 'o', Mod: tea.ModCtrl}))
			m, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: key.code}))
			if !m.AtBottom() || m.historyLive {
				t.Fatal("expected history browse with the last line visible")
			}

			beforeY := m.YOffset()
			next, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc}))
			if !strings.Contains(roomHeader(next), "LIVE") || next.activeFocus != focusInput {
				t.Fatal("expected Esc at bottom to return to compose live")
			}
			if next.YOffset() != beforeY {
				t.Fatal("expected Esc to preserve the viewport")
			}
			next = next.AppendSystem("[new]")
			if !next.AtBottom() || next.YOffset() <= beforeY {
				t.Fatal("expected compose live to follow new output")
			}
		})
	}
}

func TestStateTransitions_composeBrowseToHistoryBrowse(t *testing.T) {
	m := seededRoomModel(t)

	browsing, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyPgUp}))
	beforeY := browsing.YOffset()
	if strings.Contains(roomHeader(browsing), "LIVE") {
		t.Fatalf("expected PgUp from compose live to enter compose browse, got %q", roomHeader(browsing))
	}

	next, _ := browsing.Update(tea.KeyPressMsg(tea.Key{Code: 'o', Mod: tea.ModCtrl}))
	if strings.Contains(roomHeader(next), "LIVE") {
		t.Fatalf("expected Ctrl+O from compose browse to enter history browse, got %q", roomHeader(next))
	}
	if next.YOffset() != beforeY {
		t.Fatalf("expected history browse to preserve viewport; before=%d after=%d", beforeY, next.YOffset())
	}
	if !historyCursorRendered(next) {
		t.Fatal("expected history browse to render a visible cursor")
	}
	if next.history.CursorAtLiveEnd() {
		row, col := next.HistoryCursorPosition()
		t.Fatalf("expected history browse cursor away from live end; row=%d col=%d", row, col)
	}
}

func TestStateTransitions_historyBrowseToComposeBrowse_preservesBrowseMode(t *testing.T) {
	m := seededRoomModel(t)
	m, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 'o', Mod: tea.ModCtrl}))
	m, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyPgUp}))
	if m.AtBottom() {
		t.Fatal("expected PgUp to scroll away from the bottom")
	}

	beforeY := m.YOffset()
	next, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc}))
	if strings.Contains(roomHeader(next), "LIVE") {
		t.Fatalf("expected Esc from history browse to return to compose browse, got %q", roomHeader(next))
	}
	if historyCursorRendered(next) {
		t.Fatal("expected compose browse not to render the history cursor")
	}
	if next.YOffset() != beforeY {
		t.Fatalf("expected compose browse to preserve viewport; before=%d after=%d", beforeY, next.YOffset())
	}

	afterOutput := next.AppendSystem("[new]")
	if afterOutput.YOffset() != beforeY {
		t.Fatalf("expected compose browse to keep viewport on new output; before=%d after=%d", beforeY, afterOutput.YOffset())
	}
	if strings.Contains(roomHeader(afterOutput), "LIVE") {
		t.Fatalf("expected compose browse to stay non-live after new output, got %q", roomHeader(afterOutput))
	}
}

func TestStateTransitions_historyBrowseToHistoryLive_whenCursorReturnsToTail(t *testing.T) {
	m := seededRoomModel(t)
	m, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 'o', Mod: tea.ModCtrl}))
	m, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyUp}))
	if strings.Contains(roomHeader(m), "LIVE") {
		t.Fatalf("expected Up from history live to enter history browse, got %q", roomHeader(m))
	}

	for !m.history.CursorAtLiveEnd() {
		beforeRow, beforeCol := m.HistoryCursorPosition()
		next, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
		afterRow, afterCol := next.HistoryCursorPosition()
		if afterRow == beforeRow && afterCol == beforeCol {
			t.Fatal("expected Down to make progress toward live end")
		}
		m = next
	}
	if !strings.Contains(roomHeader(m), "LIVE") {
		t.Fatalf("expected returning cursor to tail to re-enter history live, got %q", roomHeader(m))
	}
	if !m.AtBottom() {
		t.Fatalf("expected history live to remain bottom-aligned; yOffset=%d", m.YOffset())
	}
}

func seededRoomModel(t *testing.T) Model {
	t.Helper()
	m := newTestModel(t)
	m = m.HandleResize(40, 12)
	for range 40 {
		m = m.AppendSystem("line")
	}
	return m.GoLive()
}

func historyCursorRendered(m Model) bool {
	return strings.Contains(m.renderHistoryView(), "\x1b[7m")
}
