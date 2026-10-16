package monolith

import (
	"slices"

	"github.com/roomscript/coderoom/internal/session"
)

func (state *stageState) deliveryEvents(outcome sessionOutcome) instructionSequence {
	sequence := instructionSequence{requestSnapshotInstruction{}}
	delivered := outcome.routing.Aliases(session.DeliveryDelivered)
	if len(delivered) != 0 {
		sequence = append(sequence, publishEventInstruction{event: StagedInputDispatched{
			Raw: state.raw, Routing: slices.Clone(delivered),
		}})
	}

	return sequence
}

func (state *stageState) deliveryOutcome(outcome sessionOutcome) instructionSequence {
	if outcome.err != nil {
		if !state.submissionPending {
			return instructionSequence{publishEventInstruction{event: OperationFailed{
				Raw: state.raw, Operation: "staged dispatch", Err: outcome.err,
			}}}
		}
		return instructionSequence{publishEventInstruction{event: SubmissionFailed{
			Raw: state.raw, Operation: "staged dispatch",
			Code: ErrorExecutionFailed, Err: outcome.err,
		}}}
	}
	if state.submissionPending {
		return instructionSequence{publishEventInstruction{event: SubmissionSucceeded{Raw: state.raw}}}
	}
	return nil
}
