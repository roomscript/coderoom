package interpreter

import (
	"fmt"
	"slices"

	"github.com/roomscript/coderoom/internal/promptlang"
	"github.com/roomscript/coderoom/internal/room"
)

// Addressed sends retain the session-owned policy plan. Readiness and cancellation
// use the shared stage machinery; primary-target decisions stay explicit here.
func (w *stageWorkflow) handleParticipantSendPlan(result participantSendPlanResult) instructionSequence {
	if !w.matches(result.target) {
		return nil
	}
	w.active.plan = result.plan
	w.active.routing = slices.Clone(result.targets)
	ref := w.nextRef()
	w.active.pending = ref
	return instructionSequence{readParticipantStateInstruction{target: ref}}
}

func (w *stageWorkflow) completeSendPlanning(send promptlang.Send) instructionSequence {
	if len(w.active.routing) == 0 {
		return w.rejectSendPlanning(SubmissionFailed{
			Raw: w.active.raw, Operation: "staged dispatch", Code: ErrorExecutionFailed,
			Err: errNoStageTargets,
		})
	}
	if slices.Contains(w.active.unavailable, send.Alias) {
		return w.rejectSendPlanning(unavailableStageFailure(w.active))
	}

	sequence := acceptedStageInputSequence(w.active.raw, w.active.routing)
	if len(w.active.notReadyAliases) != 0 {
		return append(sequence, w.queueStageUntilReady()...)
	}
	return append(sequence, w.startSendDispatch(send))
}

func (w *stageWorkflow) rejectSendPlanning(failure SubmissionFailed) instructionSequence {
	w.active = nil
	return instructionSequence{publishEventInstruction{event: failure}}
}

func (w *stageWorkflow) advanceWaitingSend(send promptlang.Send) instructionSequence {
	state := w.active
	state.notReadyAliases = notReadyAliases(state.readinessRequirements, state.unavailable)
	if slices.Contains(state.unavailable, send.Alias) {
		return w.discardUnavailableStage()
	}
	if len(state.notReadyAliases) != 0 {
		return instructionSequence{requestSnapshotInstruction{}}
	}
	return instructionSequence{w.startSendDispatch(send), requestSnapshotInstruction{}}
}

func (w *stageWorkflow) startSendDispatch(send promptlang.Send) executeSessionInstruction {
	state := w.active
	state.phase = stageDispatching
	state.dispatchRouting = activeAliases(state.routing, state.unavailable)
	state.pending = w.nextRef()
	return executeSessionInstruction{
		target: state.pending,
		request: executePlannedParticipantSendRequest{
			plan:    state.plan.DiscardUnavailableNoticeRecipients(state.unavailable),
			message: send.Text,
			notice:  fmt.Sprintf("@%s: %s", send.Alias, send.Text),
		},
		recordsOnSuccess: []room.Record{{Kind: room.KindUserInput, Text: state.raw}},
	}
}
