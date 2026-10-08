package interpreter

import (
	"slices"

	"github.com/roomscript/coderoom/internal/room"
)

// Recipient selection and readiness reads are synchronous preparation steps.
// Freeze the selection before reading requirements; joins do not expand it.
func (w *stageWorkflow) handleBroadcastPlan(result broadcastPlanResult) instructionSequence {
	if !w.matches(result.target) || w.active.phase != stagePlanning || w.active.broadcast == nil {
		return nil
	}
	w.active.routing = slices.Clone(result.targets)
	ref := w.nextRef()
	w.active.pending = ref
	return instructionSequence{readParticipantStateInstruction{target: ref}}
}

// prepareBroadcast validates the frozen plan, accepts input, then dispatches or
// suspends until readiness events resume it. Departed recipients are optional
// while at least one frozen recipient remains available.
func (w *stageWorkflow) prepareBroadcast(result participantStateResult) instructionSequence {
	if !w.matches(result.target) || w.active.phase != stagePlanning || w.active.broadcast == nil {
		return nil
	}
	state := w.active
	state.freezeBroadcastRequirements(result.readinessRequirements)
	if len(state.routing) == 0 || w.mustDiscard() {
		failure := unavailableStageFailure(state)
		w.active = nil
		return instructionSequence{publishEventInstruction{event: failure}}
	}
	actions := acceptedStageInputSequence(state.raw, state.routing)
	if !state.requirements.isReady() {
		return append(actions, w.retainPlanUntilReady()...)
	}
	return append(actions, w.startBroadcastDispatch())
}

func (w *stageWorkflow) resumeBroadcastOnReadiness() instructionSequence {
	if w.mustDiscard() {
		return w.discardUnavailableStage()
	}
	if !w.active.requirements.isReady() {
		return instructionSequence{requestSnapshotInstruction{}}
	}
	return instructionSequence{w.startBroadcastDispatch(), requestSnapshotInstruction{}}
}

func (w *stageWorkflow) startBroadcastDispatch() executeSessionInstruction {
	state := w.active
	state.phase = stageDispatching
	state.pending = w.nextRef()
	request := state.broadcastDeliveryRequest()
	state.dispatchRouting = slices.Clone(request.aliases)
	return executeSessionInstruction{
		target: state.pending, request: request,
		recordsOnSuccess: []room.Record{{Kind: room.KindUserInput, Text: state.raw}},
	}
}
