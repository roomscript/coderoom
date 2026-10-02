package room

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestPgUpPgDown_scrollHistoryWithoutAffectingComposer(t *testing.T) {
	m := newTestModel(t)
	m = m.HandleResize(80, 12)

	for range 40 {
		m = m.AppendSystem("[x]")
	}
	m = m.GoLive()

	m = m.SetComposeValue("draft text")
	beforeInput := m.ComposeValue()
	beforeY := m.YOffset()

	next, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyPgUp}))
	if next.ComposeValue() != beforeInput {
		t.Fatalf("expected PgUp not to mutate composer input; got %q", next.ComposeValue())
	}
	if next.YOffset() == beforeY {
		t.Fatalf("expected PgUp to scroll history; yOffset unchanged (%d)", beforeY)
	}

	next2, _ := next.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyPgDown}))
	if next2.ComposeValue() != beforeInput {
		t.Fatalf("expected PgDn not to mutate composer input; got %q", next2.ComposeValue())
	}
}

func TestHistoryFocus_preservesComposerScrollWhenReturningToHistory(t *testing.T) {
	m := newTestModel(t)
	m = m.HandleResize(20, 10)
	for range 30 {
		m = m.AppendSystem("line")
	}

	next, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: 'o', Mod: tea.ModCtrl}))
	cursorRow, _ := next.HistoryCursorPosition()

	next, _ = next.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc}))
	scrolled, _ := next.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyPgUp}))
	if scrolled.YOffset() >= cursorRow {
		t.Fatalf("expected composer PgUp to scroll cursor off-screen; yOffset=%d cursorRow=%d", scrolled.YOffset(), cursorRow)
	}
	beforeRefocusY := scrolled.YOffset()

	refocused, _ := scrolled.Update(tea.KeyPressMsg(tea.Key{Code: 'o', Mod: tea.ModCtrl}))
	if refocused.YOffset() != beforeRefocusY {
		t.Fatalf("expected returning to history focus to preserve viewport; before=%d after=%d", beforeRefocusY, refocused.YOffset())
	}
	refocusedRow, _ := refocused.HistoryCursorPosition()
	if refocusedRow < refocused.YOffset() || refocusedRow >= refocused.YOffset()+refocused.HistoryHeight() {
		t.Fatalf("expected returning to history focus to place cursor in visible viewport; row=%d yOffset=%d height=%d", refocusedRow, refocused.YOffset(), refocused.HistoryHeight())
	}
}

func TestHistoryFocus_homeEndJumpToTopBottom(t *testing.T) {
	m := newTestModel(t)
	m = m.HandleResize(12, 10)
	m = m.AppendSystem("hello world")

	// Enter history focus.
	next, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: 'o', Mod: tea.ModCtrl}))
	row, col := next.HistoryCursorPosition()
	if row == 0 && col == 0 {
		t.Fatalf("expected cursor to start at live end, got row=%d col=%d", row, col)
	}

	next, _ = next.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyHome}))
	row, col = next.HistoryCursorPosition()
	if row != 0 || col != 0 {
		t.Fatalf("expected Home to move to visible line start; got row=%d col=%d", row, col)
	}

	next, _ = next.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnd}))
	row, col = next.HistoryCursorPosition()
	if row != 0 || col == 0 {
		t.Fatalf("expected End to move to visible line end; got row=%d col=%d", row, col)
	}
}

func TestHistoryFocus_arrowKeysMoveCursorAndScroll(t *testing.T) {
	m := newTestModel(t)
	m = m.HandleResize(80, 10)
	for range 40 {
		m = m.AppendSystem("[x]")
	}
	m = m.GoLive()

	// Enter history focus.
	next, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: 'o', Mod: tea.ModCtrl}))
	beforeY := next.YOffset()
	beforeRow, _ := next.HistoryCursorPosition()

	next2, _ := next.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyUp}))
	afterRow, _ := next2.HistoryCursorPosition()
	if afterRow >= beforeRow {
		t.Fatalf("expected Up to move the cursor upward; before=%d after=%d", beforeRow, afterRow)
	}
	if next2.YOffset() == beforeY && beforeRow == afterRow {
		t.Fatalf("expected Up to move cursor and/or scroll history; row=%d yOffset=%d", afterRow, next2.YOffset())
	}
}

func TestHistoryFocus_resizePreservesExistingCursor(t *testing.T) {
	m := newTestModel(t)
	m = m.HandleResize(20, 12)
	for range 30 {
		m = m.AppendSystem("line")
	}

	m, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 'o', Mod: tea.ModCtrl}))
	m, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyUp}))
	beforeRow, beforeCol := m.HistoryCursorPosition()
	if m.AtBottom() && beforeRow == 0 && beforeCol == 0 {
		t.Fatalf("expected cursor to move away from live end before resize; row=%d col=%d", beforeRow, beforeCol)
	}

	resized := m.HandleResize(20, 11)
	afterRow, afterCol := resized.HistoryCursorPosition()
	if afterRow != beforeRow || afterCol != beforeCol {
		t.Fatalf("expected resize to preserve history cursor; before=(%d,%d) after=(%d,%d)", beforeRow, beforeCol, afterRow, afterCol)
	}
}

func TestHistoryFocus_shiftArrowStartsSelectionFromCurrentCursor(t *testing.T) {
	m := newTestModel(t)
	m = m.HandleResize(20, 12)
	m = m.AppendSystem("hello world")

	m, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 'o', Mod: tea.ModCtrl}))
	beforeRow, beforeCol := m.HistoryCursorPosition()

	m, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyLeft, Mod: tea.ModShift}))
	afterRow, afterCol := m.HistoryCursorPosition()
	if !m.HistoryHasSelection() {
		t.Fatal("expected Shift+Left to start a selection")
	}
	if afterRow != beforeRow || afterCol >= beforeCol {
		t.Fatalf("expected Shift+Left to move cursor left from anchor; before=(%d,%d) after=(%d,%d)", beforeRow, beforeCol, afterRow, afterCol)
	}
	if !strings.Contains(m.renderHistoryView(), "\x1b[48;5;238m") {
		t.Fatalf("expected selection highlight in history view, got %q", m.renderHistoryView())
	}
}

func TestHistoryFocus_ctrlShiftArrowExtendsSelectionByWord(t *testing.T) {
	m := newTestModel(t)
	m = m.HandleResize(20, 12)
	m = m.AppendSystem("one two three")

	m, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 'o', Mod: tea.ModCtrl}))
	wordSelection := tea.ModCtrl | tea.ModShift
	m, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyLeft, Mod: wordSelection}))
	if got, ok := m.HistorySelectedText(); !ok || got != "three" {
		t.Fatalf("first word selection = (%q,%v), want (%q,true)", got, ok, "three")
	}

	m, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyLeft, Mod: wordSelection}))
	if got, ok := m.HistorySelectedText(); !ok || got != "two three" {
		t.Fatalf("second word selection = (%q,%v), want (%q,true)", got, ok, "two three")
	}

	m, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyRight, Mod: wordSelection}))
	if got, ok := m.HistorySelectedText(); !ok || got != "three" {
		t.Fatalf("shrunk word selection = (%q,%v), want (%q,true)", got, ok, "three")
	}
}

func TestHistoryFocus_ctrlShiftRightStopsAtExclusiveWordBoundary(t *testing.T) {
	m := newTestModel(t)
	m = m.HandleResize(20, 12)
	m = m.AppendSystem("one two")
	m, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 'o', Mod: tea.ModCtrl}))
	m, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyHome}))

	m, _ = m.Update(tea.KeyPressMsg(tea.Key{
		Code: tea.KeyRight,
		Mod:  tea.ModCtrl | tea.ModShift,
	}))
	if got, ok := m.HistorySelectedText(); !ok || got != "one" {
		t.Fatalf("word selection = (%q,%v), want (%q,true)", got, ok, "one")
	}
}

func TestHistoryFocus_ctrlShiftLeftShrinksRightwardWordSelectionAtBoundary(t *testing.T) {
	m := newTestModel(t)
	m = m.HandleResize(20, 12)
	m = m.AppendSystem("one two three")
	m, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 'o', Mod: tea.ModCtrl}))
	m, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyHome}))
	wordSelection := tea.ModCtrl | tea.ModShift

	m, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyRight, Mod: wordSelection}))
	m, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyRight, Mod: wordSelection}))
	if got, ok := m.HistorySelectedText(); !ok || got != "one two" {
		t.Fatalf("expanded word selection = (%q,%v), want (%q,true)", got, ok, "one two")
	}

	m, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyLeft, Mod: wordSelection}))
	if got, ok := m.HistorySelectedText(); !ok || got != "one" {
		t.Fatalf("shrunk word selection = (%q,%v), want (%q,true)", got, ok, "one")
	}
}

func TestHistoryFocus_shiftLeftShrinksRightwardWordSelectionByCell(t *testing.T) {
	m := newTestModel(t)
	m = m.HandleResize(20, 12)
	m = m.AppendSystem("one two")
	m, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 'o', Mod: tea.ModCtrl}))
	m, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyHome}))
	m, _ = m.Update(tea.KeyPressMsg(tea.Key{
		Code: tea.KeyRight,
		Mod:  tea.ModCtrl | tea.ModShift,
	}))

	m, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyLeft, Mod: tea.ModShift}))
	if got, ok := m.HistorySelectedText(); !ok || got != "on" {
		t.Fatalf("mixed word/cell selection = (%q,%v), want (%q,true)", got, ok, "on")
	}
}

func TestHistoryFocus_selectionCrossesAnchorAndSelectsOppositeSide(t *testing.T) {
	m := newTestModel(t)
	m = m.HandleResize(20, 12)
	m = m.AppendSystem("hello")
	m, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 'o', Mod: tea.ModCtrl}))
	m, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyHome}))
	m, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyRight}))
	m, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyRight}))

	m, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyLeft, Mod: tea.ModShift}))
	if got, ok := m.HistorySelectedText(); !ok || got != "el" {
		t.Fatalf("left side selection = (%q,%v), want (%q,true)", got, ok, "el")
	}
	for range 2 {
		m, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyRight, Mod: tea.ModShift}))
	}
	if got, ok := m.HistorySelectedText(); !ok || got != "ll" {
		t.Fatalf("opposite side selection = (%q,%v), want (%q,true)", got, ok, "ll")
	}
}

func TestHistoryFocus_ctrlShiftLeftMovesAcrossRenderedLineBoundary(t *testing.T) {
	m := newTestModel(t)
	m = m.HandleResize(20, 12)
	m = m.AppendSystem("one\ntwo")
	m, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 'o', Mod: tea.ModCtrl}))
	wordSelection := tea.ModCtrl | tea.ModShift

	m, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyLeft, Mod: wordSelection}))
	if got, ok := m.HistorySelectedText(); !ok || got != "two" {
		t.Fatalf("last-line word selection = (%q,%v), want (%q,true)", got, ok, "two")
	}
	m, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyLeft, Mod: wordSelection}))
	if got, ok := m.HistorySelectedText(); !ok || got != "one\ntwo" {
		t.Fatalf("cross-line word selection = (%q,%v), want (%q,true)", got, ok, "one\ntwo")
	}
}

func TestHistoryFocus_ctrlShiftRightStopsAtRenderedLineBoundary(t *testing.T) {
	m := newTestModel(t)
	m = m.HandleResize(20, 12)
	m = m.AppendSystem("one\ntwo")
	m, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 'o', Mod: tea.ModCtrl}))
	m, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyUp}))
	m, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyHome}))

	m, _ = m.Update(tea.KeyPressMsg(tea.Key{
		Code: tea.KeyRight,
		Mod:  tea.ModCtrl | tea.ModShift,
	}))
	if got, ok := m.HistorySelectedText(); !ok || got != "one" {
		t.Fatalf("line-end word selection = (%q,%v), want (%q,true)", got, ok, "one")
	}
}

func TestHistoryFocus_plainMovementClearsSelection(t *testing.T) {
	m := newTestModel(t)
	m = m.HandleResize(20, 12)
	m = m.AppendSystem("hello world")

	m, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 'o', Mod: tea.ModCtrl}))
	m, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyLeft, Mod: tea.ModShift}))
	if !m.HistoryHasSelection() {
		t.Fatal("expected selection after Shift+Left")
	}

	m, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyLeft}))
	if m.HistoryHasSelection() {
		t.Fatal("expected unmodified movement to clear selection")
	}
	if strings.Contains(m.renderHistoryView(), "\x1b[48;5;238m") {
		t.Fatalf("expected selection highlight to clear, got %q", m.renderHistoryView())
	}
}

func TestHistoryFocus_escClearsSelectionBeforeLeavingHistory(t *testing.T) {
	m := newTestModel(t)
	m = m.HandleResize(20, 12)
	m = m.AppendSystem("hello world")

	m, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 'o', Mod: tea.ModCtrl}))
	m, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyLeft, Mod: tea.ModShift}))

	cleared, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc}))
	if cleared.activeFocus != focusHistory {
		t.Fatalf("expected Esc with active selection to stay in history focus, got focus=%v", cleared.activeFocus)
	}
	if cleared.HistoryHasSelection() {
		t.Fatal("expected Esc to clear active selection")
	}

	exited, _ := cleared.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc}))
	if exited.activeFocus != focusInput {
		t.Fatalf("expected second Esc to leave history focus, got focus=%v", exited.activeFocus)
	}
}

func TestHistoryFocus_shiftPageDownExtendsSelection(t *testing.T) {
	m := newTestModel(t)
	m = m.HandleResize(20, 12)
	for range 40 {
		m = m.AppendSystem("line")
	}

	m, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 'o', Mod: tea.ModCtrl}))
	m, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyPgUp}))
	beforeRow, _ := m.HistoryCursorPosition()
	m, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyPgDown, Mod: tea.ModShift}))
	afterRow, _ := m.HistoryCursorPosition()
	if !m.HistoryHasSelection() {
		t.Fatal("expected Shift+PgDn to start or extend a selection")
	}
	if afterRow <= beforeRow {
		t.Fatalf("expected Shift+PgDn to move cursor downward; before=%d after=%d", beforeRow, afterRow)
	}
}

func TestShiftPagingOutsideHistoryStillScrollsHistory(t *testing.T) {
	m := newTestModel(t)
	m = m.HandleResize(20, 12)
	for range 40 {
		m = m.AppendSystem("line")
	}

	before := m.YOffset()
	next, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyPgUp, Mod: tea.ModShift}))
	if next.YOffset() == before {
		t.Fatalf("expected Shift+PgUp in compose focus to scroll history; yOffset unchanged at %d", before)
	}

	afterUp := next.YOffset()
	next, _ = next.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyPgDown, Mod: tea.ModShift}))
	if next.YOffset() == afterUp {
		t.Fatalf("expected Shift+PgDn in compose focus to scroll history; yOffset unchanged at %d", afterUp)
	}
}

func TestHistoryFocus_selectionCanReverseDirection(t *testing.T) {
	m := newTestModel(t)
	m = m.HandleResize(20, 12)
	m = m.AppendSystem("hello world")

	m, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 'o', Mod: tea.ModCtrl}))
	startRow, startCol := m.HistoryCursorPosition()

	m, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyLeft, Mod: tea.ModShift}))
	m, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyRight, Mod: tea.ModShift}))
	row, col := m.HistoryCursorPosition()
	if row != startRow || col != startCol {
		t.Fatalf("expected reversed selection to bring cursor back to anchor; start=(%d,%d) after=(%d,%d)", startRow, startCol, row, col)
	}
	if !m.HistoryHasSelection() {
		t.Fatal("expected reversed selection state to remain active until cleared")
	}
	if strings.Contains(m.renderHistoryView(), "\x1b[48;5;238m") {
		t.Fatalf("expected collapsed reversed selection to render no highlight, got %q", m.renderHistoryView())
	}
}

func TestHistoryFocus_shiftEndKeepsTailSelectionHighlightedAtEOL(t *testing.T) {
	m := newTestModel(t)
	m = m.HandleResize(20, 12)
	m = m.AppendSystem("hello")

	m, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 'o', Mod: tea.ModCtrl}))
	m, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyLeft}))
	m, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyLeft}))
	m, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyLeft, Mod: tea.ModShift}))
	m, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnd, Mod: tea.ModShift}))

	view := m.renderHistoryView()
	if strings.Count(view, "\x1b[48;5;238m") == 0 {
		t.Fatalf("expected Shift+End tail selection to stay highlighted, got %q", view)
	}
	if !strings.Contains(view, "\x1b[7m") {
		t.Fatalf("expected EOL cursor to remain visible, got %q", view)
	}
}

func TestHistoryFocus_toggleToComposeClearsSelection(t *testing.T) {
	m := newTestModel(t)
	m = m.HandleResize(20, 12)
	m = m.AppendSystem("hello world")

	m, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 'o', Mod: tea.ModCtrl}))
	m, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyLeft, Mod: tea.ModShift}))
	if !m.HistoryHasSelection() {
		t.Fatal("expected active selection before leaving history focus")
	}

	m, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 'o', Mod: tea.ModCtrl}))
	if m.HistoryHasSelection() {
		t.Fatal("expected Ctrl+O out of history to clear selection")
	}
	if strings.Contains(m.renderHistoryView(), "\x1b[48;5;238m") {
		t.Fatalf("expected compose view to render no stale selection, got %q", m.renderHistoryView())
	}

	m, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 'o', Mod: tea.ModCtrl}))
	if m.HistoryHasSelection() {
		t.Fatal("expected re-entering history to stay selection-free")
	}
}
