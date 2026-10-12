package interpreter

import (
	"slices"

	"github.com/roomscript/coderoom/internal/room"
)

// prepareSend is synchronous: retain the frozen plan, validate it, accept input,
// then dispatch now or suspend until a participant readiness event arrives.
func (w *stageWorkflow) prepareSend(result sendPlanResult) instructionSequence {
	if !w.matches(result.target) || w.active.phase != stagePlanning {
		return nil
	}
	if w.active.send == nil {
		return nil
	}
	w.active.freezeSend(result)
	if len(w.active.targetAliases()) == 0 {
		return w.rejectSendPlanning(unavailableStageFailure(w.active))
	}
	if slices.Contains(w.active.requirements.unavailable, w.active.send.delivery.PrimaryParticipant()) {
		return w.rejectSendPlanning(unavailableStageFailure(w.active))
	}

	sequence := acceptedStageInputSequence(w.active.raw, w.active.targetAliases())
	if !w.active.requirements.isReady() {
		return append(sequence, w.retainPlanUntilReady()...)
	}
	return append(sequence, w.startSendDispatch())
}

func (w *stageWorkflow) rejectSendPlanning(failure SubmissionFailed) instructionSequence {
	w.active = nil
	return instructionSequence{publishEventInstruction{event: failure}}
}

// resumeSendOnReadiness is the entry point after a retained send returns control.
func (w *stageWorkflow) resumeSendOnReadiness() instructionSequence {
	state := w.active
	if slices.Contains(state.requirements.unavailable, state.send.delivery.PrimaryParticipant()) {
		return w.discardUnavailableStage()
	}
	if !state.requirements.isReady() {
		return instructionSequence{requestSnapshotInstruction{}}
	}
	return instructionSequence{w.startSendDispatch(), requestSnapshotInstruction{}}
}

func (w *stageWorkflow) startSendDispatch() executeSessionInstruction {
	state := w.active
	state.phase = stageDispatching
	state.pending = w.nextRef()
	return executeSessionInstruction{
		target:           state.pending,
		request:          state.send.deliveryRequest(state.requirements.unavailable),
		recordsOnSuccess: []room.Record{{Kind: room.KindUserInput, Text: state.raw}},
	}
}
