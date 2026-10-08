package interpreter

import (
	"slices"

	"github.com/roomscript/coderoom/internal/promptlang"
	"github.com/roomscript/coderoom/internal/session"
)

func (state *stageState) deliveryEvents(completion sessionCompletion) instructionSequence {
	sequence := instructionSequence{requestSnapshotInstruction{}}
	delivered := completion.routing.Aliases(session.DeliveryDelivered)
	switch state.statement.(type) {
	case promptlang.Send, promptlang.Broadcast, promptlang.Handoff:
		if len(delivered) == 0 {
			break
		}
		sequence = append(sequence, publishEventInstruction{event: StagedInputDispatched{
			Raw: state.raw, Routing: slices.Clone(delivered),
		}})
	}
	if state.handoff != nil && state.handoff.completed != nil && completion.err == nil {
		handoff := state.handoff.completed
		sequence = append(sequence, publishEventInstruction{event: HandoffCompleted{
			Preview: handoff.Preview,
		}})
	}
	return sequence
}

func (state *stageState) deliveryOutcome(completion sessionCompletion) instructionSequence {
	if completion.err != nil {
		if !state.submissionPending {
			return instructionSequence{publishEventInstruction{event: OperationFailed{
				Operation: "staged dispatch", Err: completion.err,
			}}}
		}
		return instructionSequence{publishEventInstruction{event: SubmissionFailed{
			Raw: state.raw, Operation: "staged dispatch",
			Code: ErrorExecutionFailed, Err: completion.err,
		}}}
	}
	if state.submissionPending {
		return instructionSequence{publishEventInstruction{event: SubmissionSucceeded{Raw: state.raw}}}
	}
	return nil
}
