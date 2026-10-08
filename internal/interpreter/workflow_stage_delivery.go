package interpreter

import (
	"slices"

	"github.com/roomscript/coderoom/internal/promptlang"
	"github.com/roomscript/coderoom/internal/session"
)

// Delivery completion is shared by staged commands. The runner has already
// recorded accepted input and projected causal events before this transition.
func (w *stageWorkflow) handleSessionCompletion(completion sessionCompletion) instructionSequence {
	if sequence, handled := w.handlePendingInterruptCompletion(completion); handled {
		return sequence
	}
	if !w.matches(completion.target) {
		return nil
	}
	state := w.active
	w.active = nil
	sequence := stageDispatchEventSequence(state, completion)
	return append(sequence, stageDispatchOutcomeInstruction(state, completion)...)
}

func stageDispatchEventSequence(
	state *stageState,
	completion sessionCompletion,
) instructionSequence {
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

func stageDispatchOutcomeInstruction(
	state *stageState,
	completion sessionCompletion,
) instructionSequence {
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
