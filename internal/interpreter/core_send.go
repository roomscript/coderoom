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
	if len(w.active.routing) == 0 {
		return w.rejectSendPlanning(SubmissionFailed{
			Raw: w.active.raw, Operation: "staged dispatch", Code: ErrorExecutionFailed,
			Err: errNoStageTargets,
		})
	}
	if slices.Contains(w.active.requirements.unavailable, w.active.send.action.Alias.Value) {
		return w.rejectSendPlanning(unavailableStageFailure(w.active))
	}

	sequence := acceptedStageInputSequence(w.active.raw, w.active.routing)
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
	if slices.Contains(state.requirements.unavailable, state.send.action.Alias.Value) {
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
	state.dispatchRouting = activeAliases(state.routing, state.requirements.unavailable)
	state.pending = w.nextRef()
	return executeSessionInstruction{
		target:           state.pending,
		request:          state.send.deliveryRequest(state.requirements.unavailable),
		recordsOnSuccess: []room.Record{{Kind: room.KindUserInput, Text: state.raw}},
	}
}
