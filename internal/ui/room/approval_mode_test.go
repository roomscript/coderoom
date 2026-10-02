package room

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/trigosec/coderoom/internal/interpreter"
	"github.com/trigosec/coderoom/internal/ui/room/approval"
)

func flattenCmd(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if msg == nil {
		return nil
	}
	if batch, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, sub := range batch {
			out = append(out, flattenCmd(sub)...)
		}
		return out
	}
	return []tea.Msg{msg}
}

func firstDecisionMsg(t *testing.T, msgs []tea.Msg) ApprovalDecisionMsg {
	t.Helper()
	for _, msg := range msgs {
		if decision, ok := msg.(ApprovalDecisionMsg); ok {
			return decision
		}
	}
	t.Fatalf("expected ApprovalDecisionMsg in %#v", msgs)
	return ApprovalDecisionMsg{}
}

func TestApprovalMode_enterEmitsApprovalDecisionMsgAndReturnsToCompose(t *testing.T) {
	m := newTestModel(t)
	m = m.HandleResize(80, 20)
	m = m.ShowApproval(interpreter.Approval{
		Prompt:  "approve?",
		Options: []interpreter.ApprovalOption{{ID: "decline"}, {ID: "accept"}},
	})

	next, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown})) // select accept
	if cmd != nil {
		t.Fatal("expected no cmd from navigation")
	}

	// Enter produces a ConfirmMsg, which must be fed back into Update to get a decision.
	next, cmd = next.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if cmd == nil {
		t.Fatal("expected cmd from approval confirm")
	}
	confirm := cmd()
	if _, ok := confirm.(approval.ConfirmMsg); !ok {
		t.Fatalf("expected ConfirmMsg, got %T", confirm)
	}

	next2, cmd2 := next.Update(confirm)
	if cmd2 == nil {
		t.Fatal("expected cmd producing ApprovalDecisionMsg after confirm")
	}
	decision := firstDecisionMsg(t, flattenCmd(cmd2))
	if decision.Choice.OptionID != "accept" {
		t.Fatalf("choice = %q, want accept", decision.Choice.OptionID)
	}
	if next2.input.kind != inputCompose || next2.input.approval.Active() || next2.activeFocus != focusInput {
		t.Fatal("approval confirmation did not restore compose mode and focus")
	}
	next2, _ = next2.Update(tea.KeyPressMsg(tea.Key{Code: 'x', Text: "x"}))
	if next2.ComposeValue() != "x" {
		t.Fatal("restored composer did not accept typing")
	}
}

func TestApprovalMode_escEmitsDeclineDecisionMsg(t *testing.T) {
	m := newTestModel(t)
	m = m.HandleResize(80, 20)
	m = m.ShowApproval(interpreter.Approval{
		Prompt:  "approve?",
		Options: []interpreter.ApprovalOption{{ID: "decline"}, {ID: "accept"}},
	})

	next, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc}))
	if cmd == nil {
		t.Fatal("expected cmd from Esc cancel")
	}
	cancel := cmd()
	if _, ok := cancel.(approval.CancelMsg); !ok {
		t.Fatalf("expected CancelMsg, got %T", cancel)
	}

	next2, cmd2 := next.Update(cancel)
	if cmd2 == nil {
		t.Fatal("expected cmd producing ApprovalDecisionMsg after cancel")
	}
	decision := firstDecisionMsg(t, flattenCmd(cmd2))
	if decision.Choice.OptionID != "decline" {
		t.Fatalf("choice = %q, want decline", decision.Choice.OptionID)
	}
	if next2.input.kind != inputCompose || next2.input.approval.Active() || next2.activeFocus != focusInput {
		t.Fatal("approval cancellation did not restore compose mode and focus")
	}
	next2, _ = next2.Update(tea.KeyPressMsg(tea.Key{Code: 'x', Text: "x"}))
	if next2.ComposeValue() != "x" {
		t.Fatal("restored composer did not accept typing")
	}
}

func TestApprovalMode_ctrlCEmitsCancelDecisionMsg(t *testing.T) {
	m := newTestModel(t)
	m = m.HandleResize(80, 20)
	m = m.ShowApproval(interpreter.Approval{
		Prompt:  "approve?",
		Options: []interpreter.ApprovalOption{{ID: "accept"}, {ID: "cancel"}, {ID: "decline"}},
	})

	next, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: 'c', Mod: tea.ModCtrl}))
	if cmd == nil {
		t.Fatal("expected cmd from Ctrl+C cancel")
	}

	msgs := flattenCmd(cmd)
	decision := firstDecisionMsg(t, msgs)
	if decision.Choice.OptionID != "cancel" {
		t.Fatalf("choice = %q, want %q", decision.Choice.OptionID, "cancel")
	}
	if next.ComposeValue() != "" {
		t.Fatalf("expected return to compose mode, got %q", next.ComposeValue())
	}
}

func TestApprovalMode_ctrlCRestoresStagedComposer(t *testing.T) {
	m := newTestModel(t)
	m = m.HandleResize(80, 20)
	m = m.SetComposerStaged("next turn", "Participants busy: ada.")
	m = m.ShowApproval(interpreter.Approval{
		Prompt:  "approve?",
		Options: []interpreter.ApprovalOption{{ID: "accept"}, {ID: "cancel"}, {ID: "decline"}},
	})

	next, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: 'c', Mod: tea.ModCtrl}))
	if cmd == nil {
		t.Fatal("expected cmd from Ctrl+C cancel")
	}

	decision := firstDecisionMsg(t, flattenCmd(cmd))
	if decision.Choice.OptionID != "cancel" {
		t.Fatalf("choice = %q, want %q", decision.Choice.OptionID, "cancel")
	}
	if !next.IsComposerStaged() {
		t.Fatal("expected staged composer to be restored after approval cancel")
	}
	if got := next.ComposeValue(); got != "next turn" {
		t.Fatalf("restored staged text = %q, want next turn", got)
	}
	if view := next.View(); !strings.Contains(view, "Participants busy: ada.") {
		t.Fatalf("restored stage status missing: %s", view)
	}
}

func TestApprovalMode_cancelRestoresHistorySelectionState(t *testing.T) {
	m := prepareHistorySelectionForApproval(t)
	beforeRow, beforeCol := m.HistoryCursorPosition()
	beforeText := requireHistorySelectedText(t, m)

	m = showApprovalAndRequireInputFocus(t, m, interpreter.Approval{
		Prompt:  "approve?",
		Options: []interpreter.ApprovalOption{{ID: "decline"}, {ID: "accept"}},
	})

	next := cancelApproval(t, m)
	assertHistoryFocusAndSelection(t, next)
	assertHistoryCursorPosition(t, next, beforeRow, beforeCol)
	assertHistorySelectedText(t, next, beforeText)
}

func prepareHistorySelectionForApproval(t *testing.T) Model {
	t.Helper()

	m := newTestModel(t)
	m = m.HandleResize(20, 12)
	m = m.AppendSystem("hello world")
	m, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 'o', Mod: tea.ModCtrl}))
	m, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyLeft, Mod: tea.ModShift}))
	m, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyLeft, Mod: tea.ModShift}))
	return m
}

func requireHistorySelectedText(t *testing.T, m Model) string {
	t.Helper()

	selected, ok := m.HistorySelectedText()
	if !ok {
		t.Fatal("expected active history selection before approval")
	}
	return selected
}

func showApprovalAndRequireInputFocus(t *testing.T, m Model, req interpreter.Approval) Model {
	t.Helper()

	m = m.ShowApproval(req)
	if m.activeFocus != focusInput {
		t.Fatalf("expected approval to temporarily take input focus, got %v", m.activeFocus)
	}
	return m
}

func cancelApproval(t *testing.T, m Model) Model {
	t.Helper()

	next, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc}))
	if cmd == nil {
		t.Fatal("expected cmd from Esc cancel")
	}
	cancel := cmd()
	if _, ok := cancel.(approval.CancelMsg); !ok {
		t.Fatalf("expected CancelMsg, got %T", cancel)
	}
	next, _ = next.Update(cancel)
	return next
}

func assertHistoryFocusAndSelection(t *testing.T, m Model) {
	t.Helper()

	if m.activeFocus != focusHistory {
		t.Fatalf("expected history focus to be restored after approval, got %v", m.activeFocus)
	}
	if !m.HistoryHasSelection() {
		t.Fatal("expected history selection to remain active after approval")
	}
}

func assertHistoryCursorPosition(t *testing.T, m Model, wantRow, wantCol int) {
	t.Helper()

	afterRow, afterCol := m.HistoryCursorPosition()
	if afterRow != wantRow || afterCol != wantCol {
		t.Fatalf("expected approval cancel to preserve history cursor; before=(%d,%d) after=(%d,%d)", wantRow, wantCol, afterRow, afterCol)
	}
}

func assertHistorySelectedText(t *testing.T, m Model, want string) {
	t.Helper()

	afterText, ok := m.HistorySelectedText()
	if !ok || afterText != want {
		t.Fatalf("expected approval cancel to preserve selected text; before=%q after=%q active=%v", want, afterText, ok)
	}
}
