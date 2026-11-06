package room

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestGotoBottom_preservesHistoryCursor(t *testing.T) {
	m := newTestModel(t)
	m = m.HandleResize(20, 12)
	for range 40 {
		m = m.AppendSystem("line")
	}

	m, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 'o', Mod: tea.ModCtrl}))
	m, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyUp}))
	beforeRow, beforeCol := m.HistoryCursorPosition()

	m = m.GotoBottom()
	afterRow, afterCol := m.HistoryCursorPosition()
	if afterRow != beforeRow || afterCol != beforeCol {
		t.Fatalf("expected GotoBottom to preserve cursor; before=(%d,%d) after=(%d,%d)", beforeRow, beforeCol, afterRow, afterCol)
	}
	if !m.AtBottom() {
		t.Fatal("expected GotoBottom to scroll viewport to bottom")
	}
}

func TestGoLive_rearmsFollowFromBrowseMode(t *testing.T) {
	m := newTestModel(t)
	m = m.HandleResize(40, 12)
	for range 40 {
		m = m.AppendSystem("line")
	}

	m, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 'o', Mod: tea.ModCtrl}))
	m, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyPgUp}))
	m, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc}))
	m = m.GotoBottom()

	if strings.Contains(roomHeader(m), "LIVE") {
		t.Fatalf("expected GotoBottom to preserve browse mode, got %q", roomHeader(m))
	}

	m = m.GoLive()
	if !strings.Contains(roomHeader(m), "LIVE") {
		t.Fatalf("expected GoLive to re-arm follow, got %q", roomHeader(m))
	}

	beforeY := m.YOffset()
	m = m.AppendSystem("[new]")
	if !m.AtBottom() {
		t.Fatalf("expected GoLive to keep history at bottom after new output; yOffset=%d", m.YOffset())
	}
	if m.YOffset() < beforeY {
		t.Fatalf("expected new output not to move viewport upward; before=%d after=%d", beforeY, m.YOffset())
	}
}
