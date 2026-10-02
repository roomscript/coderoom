package room

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func assertCmdContainsStagedEditMsg(t *testing.T, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		t.Fatal("expected StagedEditMsg cmd, got nil")
	}
	msg := cmd()
	switch msg := msg.(type) {
	case tea.BatchMsg:
		for _, c := range msg {
			if c == nil {
				continue
			}
			if _, ok := c().(StagedEditMsg); ok {
				return
			}
		}
		t.Fatalf("expected StagedEditMsg in batch, got %T (%v)", msg, msg)
	case StagedEditMsg:
		return
	default:
		t.Fatalf("expected StagedEditMsg, got %T", msg)
	}
}

func TestEnter_emitsSubmitMsgAndClearsComposer(t *testing.T) {
	m := newTestModel(t)
	m = m.HandleResize(80, 20)
	m = m.SetComposeValue("hello")

	next, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if got := next.ComposeValue(); got != "" {
		t.Fatalf("expected Enter to clear composer immediately, got %q", got)
	}
	if cmd == nil {
		t.Fatal("expected a SubmitMsg command from Enter")
	}
	msg := cmd()
	submit, ok := msg.(SubmitMsg)
	if !ok {
		t.Fatalf("expected SubmitMsg, got %T", msg)
	}
	if submit.Text != "hello" {
		t.Fatalf("expected SubmitMsg text %q, got %q", "hello", submit.Text)
	}
}

func TestEnter_secondPressDoesNotEmitDuplicateSubmit(t *testing.T) {
	m := newTestModel(t)
	m = m.HandleResize(80, 20)
	m = m.SetComposeValue("hello")

	next, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if cmd == nil {
		t.Fatal("expected first Enter to emit SubmitMsg")
	}
	next2, cmd2 := next.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if cmd2 != nil {
		msg := cmd2()
		t.Fatalf("expected second Enter to emit no command, got %T (%v)", msg, msg)
	}
	if got := next2.ComposeValue(); got != "" {
		t.Fatalf("expected composer to stay cleared after second Enter, got %q", got)
	}
}

func TestEnter_whitespaceOnlyDoesNotEmitSubmitMsg(t *testing.T) {
	m := newTestModel(t)
	m = m.HandleResize(80, 20)
	m = m.SetComposeValue("   \n\t ")

	next, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if got := next.ComposeValue(); got == "" {
		t.Fatalf("expected Enter not to clear composer for whitespace-only input")
	}
	if cmd != nil {
		msg := cmd()
		t.Fatalf("expected no SubmitMsg for whitespace-only input, got %T (%v)", msg, msg)
	}
}

func TestCtrlC_clearsComposerOnlyWhenFocused(t *testing.T) {
	m := newTestModel(t)
	m = m.HandleResize(80, 20)
	m = m.SetComposeValue("draft")

	next, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: 'c', Mod: tea.ModCtrl}))
	if cmd != nil {
		t.Fatalf("expected Ctrl+C to return nil cmd, got non-nil")
	}
	if got := next.ComposeValue(); got != "" {
		t.Fatalf("expected Ctrl+C to clear composer, got %q", got)
	}

	// Switch to history focus.
	next, _ = next.Update(tea.KeyPressMsg(tea.Key{Code: 'o', Mod: tea.ModCtrl}))
	next = next.SetComposeValue("draft2")

	next2, _ := next.Update(tea.KeyPressMsg(tea.Key{Code: 'c', Mod: tea.ModCtrl}))
	if got := next2.ComposeValue(); got != "draft2" {
		t.Fatalf("expected Ctrl+C not to mutate composer in history focus, got %q", got)
	}
}

func TestCtrlC_historyFocusCopiesActiveSelection(t *testing.T) {
	m := newTestModel(t)
	m = m.HandleResize(20, 12)
	m = m.AppendSystem("hello world")

	var copied string
	m.clipboardWrite = func(text string) error {
		copied = text
		return nil
	}

	m, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 'o', Mod: tea.ModCtrl}))
	m, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyLeft, Mod: tea.ModShift}))
	m, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyLeft, Mod: tea.ModShift}))

	next, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: 'c', Mod: tea.ModCtrl}))
	if copied != "ld" {
		t.Fatalf("copied text = %q, want %q", copied, "ld")
	}
	if !next.HistoryHasSelection() {
		t.Fatal("expected copy to preserve active selection")
	}
}

func TestCtrlC_historyFocusCopiesCursorCellWhenSelectionExtendsRight(t *testing.T) {
	m := newTestModel(t)
	m = m.HandleResize(20, 12)
	m = m.AppendSystem("hello world")

	var copied string
	m.clipboardWrite = func(text string) error {
		copied = text
		return nil
	}

	m, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 'o', Mod: tea.ModCtrl}))
	m, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyHome}))
	m, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyRight}))
	m, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyRight, Mod: tea.ModShift}))
	m, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyRight, Mod: tea.ModShift}))

	next, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: 'c', Mod: tea.ModCtrl}))
	if copied != "ell" {
		t.Fatalf("copied text = %q, want %q", copied, "ell")
	}
	if !next.HistoryHasSelection() {
		t.Fatal("expected copy to preserve active selection")
	}
}

func TestCtrlC_historyFocusWithoutSelectionDoesNothing(t *testing.T) {
	m := newTestModel(t)
	m = m.HandleResize(20, 12)
	m = m.AppendSystem("hello world")

	calls := 0
	m.clipboardWrite = func(_ string) error {
		calls++
		return nil
	}

	m, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 'o', Mod: tea.ModCtrl}))
	next, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: 'c', Mod: tea.ModCtrl}))
	if calls != 0 {
		t.Fatalf("expected no clipboard writes without a selection, got %d", calls)
	}
	if next.HistoryHasSelection() {
		t.Fatal("expected copy without selection not to create a selection")
	}
}

func TestCtrlV_composeFocusPastesClipboardContent(t *testing.T) {
	m := newTestModel(t)
	m = m.HandleResize(40, 12)
	m = m.SetComposeValue("hello ")
	calls := 0
	m.clipboardRead = func() (string, error) {
		calls++
		return "world\nagain", nil
	}

	next, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: 'v', Mod: tea.ModCtrl}))
	if calls != 1 {
		t.Fatalf("clipboard read calls = %d, want 1", calls)
	}
	if got := next.ComposeValue(); got != "hello world\nagain" {
		t.Fatalf("compose value = %q, want %q", got, "hello world\nagain")
	}
}

func TestCtrlShiftV_composeFocusPastesClipboardContent(t *testing.T) {
	m := newTestModel(t)
	m = m.HandleResize(40, 12)
	calls := 0
	m.clipboardRead = func() (string, error) {
		calls++
		return "paste", nil
	}

	next, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: 'v', Mod: tea.ModCtrl | tea.ModShift}))
	if calls != 1 {
		t.Fatalf("clipboard read calls = %d, want 1", calls)
	}
	if got := next.ComposeValue(); got != "paste" {
		t.Fatalf("compose value = %q, want %q", got, "paste")
	}
}

func TestCtrlV_composeFocusReportsClipboardReadError(t *testing.T) {
	m := newTestModel(t)
	m = m.HandleResize(40, 12)
	m.clipboardRead = func() (string, error) {
		return "", errors.New("clipboard unavailable")
	}

	next, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: 'v', Mod: tea.ModCtrl}))
	if cmd != nil {
		t.Fatalf("expected Ctrl+V read failure not to emit a command, got %v", cmd)
	}
	if got := next.ComposeValue(); got != "" {
		t.Fatalf("compose value = %q, want empty", got)
	}
	content := ansi.Strip(next.HistoryRenderedContent())
	content = strings.ReplaceAll(content, "\n", " ")
	if !strings.Contains(content, "error: paste failed: clipboard unavailable") {
		t.Fatalf("expected paste failure in history, got %q", content)
	}
}

func TestComposerHeight_isCappedAndDoesNotCollapseHistory(t *testing.T) {
	m := newTestModel(t)
	m = m.HandleResize(80, 30) // max input height = min(8, 30/3=10) => 8

	m = m.SetComposeValue(strings.Repeat("x\n", 20) + "x") // 21 lines
	if got := m.ComposeHeight(); got != 8 {
		t.Fatalf("expected compose height capped at 8, got %d", got)
	}
	if m.HistoryHeight() <= 0 {
		t.Fatalf("expected history height to stay positive, got %d", m.HistoryHeight())
	}
}

func TestComposeResize_preservesBottomAnchor(t *testing.T) {
	m := newTestModel(t)
	m = m.HandleResize(80, 12)
	for range 50 {
		m = m.AppendSystem("line")
	}
	m = m.GoLive()
	if !m.AtBottom() {
		t.Fatal("expected to start at bottom")
	}

	m = m.SetComposeValue("a\nb\nc")
	if !m.AtBottom() {
		t.Fatal("expected compose resize to keep history anchored to bottom")
	}
}

func TestComposeResize_preservesExistingHistoryCursor(t *testing.T) {
	m := newTestModel(t)
	m = m.HandleResize(20, 12)
	for range 30 {
		m = m.AppendSystem("line")
	}

	m, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 'o', Mod: tea.ModCtrl}))
	m, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyUp}))
	beforeRow, beforeCol := m.HistoryCursorPosition()

	m, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc}))
	m = m.SetComposeValue("a\nb\nc")
	afterRow, afterCol := m.HistoryCursorPosition()
	if afterRow != beforeRow || afterCol != beforeCol {
		t.Fatalf("expected composer resize to preserve history cursor; before=(%d,%d) after=(%d,%d)", beforeRow, beforeCol, afterRow, afterCol)
	}
}

func TestComposeResize_whenBottomAnchoredInComposer_ignoresHiddenStaleCursor(t *testing.T) {
	m := newTestModel(t)
	m = m.HandleResize(20, 12)
	for range 60 {
		m = m.AppendSystem("line")
	}

	m, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 'o', Mod: tea.ModCtrl}))
	m, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyUp}))
	staleRow, staleCol := m.HistoryCursorPosition()

	m, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc}))
	for !m.AtBottom() {
		var cmd tea.Cmd
		m, cmd = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyPgDown}))
		if cmd != nil {
			t.Fatalf("expected PgDown not to emit a command in composer focus")
		}
	}
	if !m.AtBottom() {
		t.Fatal("expected PgDown to return history to the bottom in composer focus")
	}
	beforeRow, beforeCol := m.HistoryCursorPosition()
	if beforeRow != staleRow || beforeCol != staleCol {
		t.Fatalf("expected composer scroll to bottom not to rewrite hidden cursor; before=(%d,%d) stale=(%d,%d)", beforeRow, beforeCol, staleRow, staleCol)
	}

	m = m.SetComposeValue("a\nb\nc")
	if !m.AtBottom() {
		t.Fatal("expected compose resize to keep history anchored to bottom even with a hidden stale cursor")
	}
}

func TestStagedStatusResize_preservesBottomAnchor(t *testing.T) {
	m := newTestModel(t)
	m = m.HandleResize(80, 12)
	for range 50 {
		m = m.AppendSystem("line")
	}
	m = m.GoLive()
	if !m.AtBottom() {
		t.Fatal("expected to start at bottom")
	}

	m = m.SetComposerStaged("@a hi", "Participants busy: a.")
	if !m.AtBottom() {
		t.Fatal("expected staged status line resize to keep history anchored to bottom")
	}
}

func TestStagedComposer_blocksKeysAndEscEmitsEditMsg(t *testing.T) {
	m := newTestModel(t)
	m = m.HandleResize(80, 20)
	m = m.SetComposerStaged("hello", "Message on-hold.")

	// Random typing is ignored.
	next, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: 'x', Text: "x"}))
	if cmd != nil {
		t.Fatalf("expected no cmd from typing while staged, got non-nil")
	}
	if got := next.ComposeValue(); got != "hello" {
		t.Fatalf("expected staged text unchanged, got %q", got)
	}

	// Esc exits staged mode and emits StagedEditMsg.
	next2, cmd2 := next.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc}))
	if next2.IsComposerStaged() {
		t.Fatal("expected staged mode cleared after Esc")
	}
	assertCmdContainsStagedEditMsg(t, cmd2)
	if got := next2.ComposeValue(); got != "hello" {
		t.Fatalf("expected draft to preserve staged text for editing, got %q", got)
	}
}

func TestClearComposerStaged_restoresComposerFocus(t *testing.T) {
	m := newTestModel(t)
	m = m.HandleResize(80, 20)

	m = m.SetComposerStaged("@a hi", "Message on-hold.")
	next := m.ClearComposerStaged().SetComposeValue("")
	if next.IsComposerStaged() {
		t.Fatal("expected staged mode cleared after dispatch")
	}

	// Typing should work again after auto-dispatch clears staging.
	next2, _ := next.Update(tea.KeyPressMsg(tea.Key{Code: 'x', Text: "x"}))
	if got := next2.ComposeValue(); got != "x" {
		t.Fatalf("expected composer to accept input after dispatch, got %q", got)
	}
}
