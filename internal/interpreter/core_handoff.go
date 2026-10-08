package interpreter

import (
	"slices"

	"github.com/roomscript/coderoom/internal/room"
	"github.com/roomscript/coderoom/internal/session"
)

// prepareHandoff freezes recipients and source requirements, validates the plan,
// then accepts input. Wait for readiness and completed source output, or read the
// source now. The source read is synchronous; retaining the plan suspends work.
func (w *stageWorkflow) prepareHandoff(result participantStateResult) instructionSequence {
	if !w.matches(result.target) || w.active.phase != stagePlanning || w.active.handoff == nil {
		return nil
	}
	state := w.active
	state.freezeHandoffRequirements(state.handoff.routing(), result.readinessRequirements)
	state.handoff.trackSourceCompletion(state.requirements.participants)
	if w.mustDiscard() {
		failure := unavailableStageFailure(state)
		w.active = nil
		return instructionSequence{publishEventInstruction{event: failure}}
	}
	actions := acceptedStageInputSequence(state.raw, state.routing)
	if !w.readyForHandoffSource() {
		return append(actions, w.retainPlanUntilReady()...)
	}
	return append(actions, w.readHandoffSourceInstruction())
}

func (w *stageWorkflow) readHandoffSourceInstruction() readHandoffSourceInstruction {
	state := w.active
	state.phase = stageReadingHandoffSource
	ref := w.nextRef()
	state.pending = ref
	handoff := state.handoff.action
	return readHandoffSourceInstruction{target: ref, alias: handoff.FromAlias}
}

func (w *stageWorkflow) handleHandoffSource(result handoffSourceResult) instructionSequence {
	if !w.matches(result.target) || w.active.phase != stageReadingHandoffSource {
		return nil
	}
	if !result.ok {
		return w.failHandoffSource()
	}
	return instructionSequence{w.startHandoffDispatch(result.source)}
}

func (w *stageWorkflow) startHandoffDispatch(source session.HandoffSource) executeSessionInstruction {
	state := w.active
	state.phase = stageDispatching
	state.dispatchRouting = activeAliases(state.routing, state.requirements.unavailable)
	state.pending = w.nextRef()
	return executeSessionInstruction{
		target:  state.pending,
		request: state.handoff.deliveryRequest(source, state.requirements.requiredReadyAliases()),
		recordsOnSuccess: []room.Record{{
			Kind: room.KindUserInput, Text: state.raw, Routing: slices.Clone(state.dispatchRouting),
		}},
	}
}

func (w *stageWorkflow) failHandoffSource() instructionSequence {
	state := w.active
	w.active = nil
	sequence := instructionSequence{requestSnapshotInstruction{}}
	if !state.submissionPending {
		return append(sequence, publishEventInstruction{event: OperationFailed{
			Operation: "handoff source", Err: errNoHandoffSource,
		}})
	}
	return append(sequence, publishEventInstruction{event: SubmissionFailed{
		Raw: state.raw, Operation: "handoff source",
		Code: ErrorExecutionFailed, Err: errNoHandoffSource,
	}})
}

// Readiness owns participant facts; handoff owns the source-turn requirement.
// Apply both before deciding whether the retained plan can resume.
func (w *stageWorkflow) handleHandoffSessionEvent(event session.Event) instructionSequence {
	state := w.active
	switch event := event.(type) {
	case session.AgentMessage:
		if !state.handoff.completeSourceTurn(event, state.requirements.turnID(event.Alias)) {
			return nil
		}
		state.requirements.updateTurnID(event.Alias, event.TurnID)
	case session.ParticipantStatusChanged:
		state.requirements.applySessionEvent(event)
		state.handoff.updateSourceStatus(event)
	default:
		if !state.requirements.applySessionEvent(event) {
			return nil
		}
	}
	return w.resumeHandoffOnRequirements()
}

func (w *stageWorkflow) readyForHandoffSource() bool {
	return w.active.requirements.isReady() && !w.active.handoff.sourceNeedsCompletion
}

// resumeHandoffOnRequirements follows a later readiness or source-output event.
func (w *stageWorkflow) resumeHandoffOnRequirements() instructionSequence {
	if w.mustDiscard() {
		return w.discardUnavailableStage()
	}
	if !w.readyForHandoffSource() {
		return instructionSequence{requestSnapshotInstruction{}}
	}
	return instructionSequence{w.readHandoffSourceInstruction(), requestSnapshotInstruction{}}
}
