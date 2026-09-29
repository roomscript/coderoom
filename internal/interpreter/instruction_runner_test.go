package interpreter

import (
	"testing"

	"github.com/trigosec/coderoom/internal/session"
)

func TestInstructionRunner_discardsStaleApprovalClear(t *testing.T) {
	model := newInterpreterModel()
	t.Cleanup(model.Close)
	model.approval = &Approval{ID: 7}
	runner := newInstructionRunner(model, &interpreterExecutor{})

	sequence := runner.ApplySessionEvents([]session.Event{
		session.ApprovalCleared{ID: 8},
	})

	if model.approval == nil || model.approval.ID != 7 {
		t.Fatalf("approval = %#v, want ID 7", model.approval)
	}
	if len(sequence) != 0 {
		t.Fatalf("instructions = %d, want 0", len(sequence))
	}
}
