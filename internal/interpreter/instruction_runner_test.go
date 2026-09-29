package interpreter

import (
	"testing"

	"github.com/trigosec/coderoom/internal/room"
	"github.com/trigosec/coderoom/internal/session"
)

type recordingInstructionModel struct{ events []session.Event }

func (*recordingInstructionModel) ApplyCompletion(workflowCompletion) instructionSequence {
	return nil
}

func (m *recordingInstructionModel) ApplySessionEvent(event session.Event) instructionSequence {
	m.events = append(m.events, event)
	return instructionSequence{publishEventInstruction{event: LoopStatus{Message: "unexpected"}}}
}

func (*recordingInstructionModel) AppendRecord(room.Record) {}

func TestInstructionRunner_discardsStaleApprovalClearBeforeModel(t *testing.T) {
	model := &recordingInstructionModel{}
	executor := &Interpreter{approval: &Approval{ID: 7}}
	runner := newInstructionRunner(model, executor)

	sequence := runner.ApplySessionEvents([]session.Event{
		session.ApprovalCleared{ID: 8},
	})

	if len(model.events) != 0 {
		t.Fatalf("model received stale approval clear: %#v", model.events)
	}
	if len(sequence) != 0 {
		t.Fatalf("instructions = %d, want 0", len(sequence))
	}
}
