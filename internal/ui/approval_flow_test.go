package ui

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/trigosec/coderoom/internal/agent"
	"github.com/trigosec/coderoom/internal/interpreter"
	"github.com/trigosec/coderoom/internal/session"
	uiroom "github.com/trigosec/coderoom/internal/ui/room"
	"github.com/trigosec/coderoom/internal/ui/room/history/record"
)

func TestApprovalDecision_UsesInterpreterAndRendersFailure(t *testing.T) {
	m := makeReadyModel(t)
	m.activeApprovalID = 7

	next, _ := m.handleApprovalDecision(uiroom.ApprovalDecisionMsg{Choice: interpreter.ApprovalChoice{OptionID: "accept"}})
	m = next.(Model)
	if m.activeApprovalID != 0 {
		t.Fatalf("active approval id = %d, want 0 after enqueue", m.activeApprovalID)
	}
	event, ok := m.interpreterQueue.PullTimeout(2 * time.Second)
	if !ok {
		t.Fatal("timed out waiting for approval resolution failure")
	}
	failed, ok := event.(interpreter.OperationFailed)
	if !ok {
		t.Fatalf("event = %T, want OperationFailed", event)
	}
	m, _ = m.handleInterpreterEvent(failed)
	if !hasRecord(m, record.KindSystem, "error: resolve approval") {
		t.Fatalf("approval failure was not rendered: %v", m.room.HistoryRecords())
	}
}

func TestApprovalDecision_ShutdownFailureKeepsActiveApproval(t *testing.T) {
	m := makeReadyModel(t)
	m.activeApprovalID = 7
	m.interpreter.Close()

	next, _ := m.handleApprovalDecision(uiroom.ApprovalDecisionMsg{Choice: interpreter.ApprovalChoice{OptionID: "accept"}})
	m = next.(Model)
	if m.activeApprovalID != 7 {
		t.Fatalf("active approval id = %d, want 7", m.activeApprovalID)
	}
	if !hasRecord(m, record.KindSystem, "error: resolve approval") {
		t.Fatalf("shutdown failure was not rendered: %v", m.room.HistoryRecords())
	}
}

func TestOperationFailed_RendersInterpreterError(t *testing.T) {
	m := makeReadyModel(t)

	m, _ = m.handleInterpreterEvent(interpreter.OperationFailed{
		Operation: "resolve approval",
		Err:       errors.New("failed"),
	})

	if !hasRecord(m, record.KindSystem, "error: resolve approval") {
		t.Fatalf("operation failure was not rendered: %v", m.room.HistoryRecords())
	}
}

func TestApprovalEvents_ClearActivePrompt(t *testing.T) {
	m := makeReadyModel(t)
	m = pushEvent(m, session.ApprovalRequested{
		Alias: "ada",
		ID:    7,
		Req: agent.ApprovalRequest{
			Ask:     "approve?",
			Options: []agent.ApprovalOption{agent.OptionAccept, agent.OptionDecline},
		},
	})

	if !strings.Contains(m.room.View(), "approve?") {
		t.Fatalf("expected approval prompt in room view, got:\n%s", m.room.View())
	}

	m = pushEvent(m, session.ApprovalCleared{ID: 7})

	if strings.Contains(m.room.View(), "approve?") {
		t.Fatalf("expected cleared approval prompt to disappear, got:\n%s", m.room.View())
	}
	if m.activeApprovalID != 0 {
		t.Fatalf("active approval id = %d, want 0", m.activeApprovalID)
	}
}

func TestApprovalEvents_IgnoreClearForDifferentApproval(t *testing.T) {
	m := makeReadyModel(t)
	m = pushEvent(m, session.ApprovalRequested{
		Alias: "ada",
		ID:    7,
		Req: agent.ApprovalRequest{
			Ask:     "approve?",
			Options: []agent.ApprovalOption{agent.OptionAccept, agent.OptionDecline},
		},
	})

	m = pushEvent(m, session.ApprovalCleared{ID: 8})

	if !strings.Contains(m.room.View(), "approve?") {
		t.Fatalf("expected mismatched clear event to preserve the visible prompt, got:\n%s", m.room.View())
	}
	if m.activeApprovalID != 7 {
		t.Fatalf("active approval id = %d, want 7", m.activeApprovalID)
	}
}

func TestApprovalEvents_ClearRestoresStagedComposer(t *testing.T) {
	m := makeReadyModel(t)
	m.room = m.room.SetComposerStaged("next turn", "Participants busy: ada.")

	if !m.room.IsComposerStaged() {
		t.Fatal("expected staged composer before approval prompt")
	}

	m = pushEvent(m, session.ApprovalRequested{
		Alias: "ada",
		ID:    7,
		Req: agent.ApprovalRequest{
			Ask:     "approve?",
			Options: []agent.ApprovalOption{agent.OptionAccept, agent.OptionDecline},
		},
	})
	m = pushEvent(m, session.ApprovalCleared{ID: 7})

	if !m.room.IsComposerStaged() {
		t.Fatal("expected staged composer to be restored after approval clear")
	}
	if got := m.room.ComposeValue(); got != "next turn" {
		t.Fatalf("restored staged text = %q, want next turn", got)
	}
	if view := m.room.View(); !strings.Contains(view, "Participants busy: ada.") {
		t.Fatalf("restored stage status missing: %s", view)
	}
	if strings.Contains(m.room.View(), "approve?") {
		t.Fatalf("expected cleared approval prompt to disappear, got:\n%s", m.room.View())
	}
}
