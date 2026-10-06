package approval

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/roomscript/coderoom/internal/interpreter"
)

func TestUpdate_downSelectsNextOption(t *testing.T) {
	m := New().Set(interpreter.Approval{
		Prompt:  "approve?",
		Options: []interpreter.ApprovalOption{{ID: "decline"}, {ID: "accept"}},
	})
	if got := m.Selected(); got != 0 {
		t.Fatalf("expected selected=0, got %d", got)
	}
	m2, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	if got := m2.Selected(); got != 1 {
		t.Fatalf("expected selected=1 after Down, got %d", got)
	}
}

func TestUpdate_enterEmitsConfirmMsg(t *testing.T) {
	m := New().Set(interpreter.Approval{
		Prompt:  "approve?",
		Options: []interpreter.ApprovalOption{{ID: "decline"}},
	})
	_, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if cmd == nil {
		t.Fatal("expected confirm cmd")
	}
	if _, ok := cmd().(ConfirmMsg); !ok {
		t.Fatalf("expected ConfirmMsg, got %T", cmd())
	}
}

func TestSet_preservesDetachedOptionsAndSelectedID(t *testing.T) {
	options := []interpreter.ApprovalOption{{ID: "custom", Label: "Custom response"}}
	m := New().Set(interpreter.Approval{Prompt: "approve?", Options: options})
	options[0] = interpreter.ApprovalOption{ID: "changed", Label: "Changed"}
	choice, ok := m.SelectedOption()
	if !ok || choice.OptionID != "custom" {
		t.Fatalf("choice = %#v, ok = %v", choice, ok)
	}
	if got := m.View(); got != "approve?\n\n> Custom response" {
		t.Fatalf("view = %q", got)
	}
}
