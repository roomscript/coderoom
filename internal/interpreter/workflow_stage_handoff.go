package interpreter

import (
	"slices"

	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/promptlang"
	"github.com/roomscript/coderoom/internal/room"
	"github.com/roomscript/coderoom/internal/session"
)

// handoffStage exists only for a handoff. Sends and broadcasts do not own source
// output tracking or accepted-context state.
type handoffStage struct {
	sourceNeedsCompletion bool
	completed             *session.HandoffDelivered
}

func (w *stageWorkflow) readHandoffSourceInstruction() readHandoffSourceInstruction {
	state := w.active
	state.phase = stageReadingHandoffSource
	ref := w.nextRef()
	state.pending = ref
	handoff := state.statement.(promptlang.Handoff)
	return readHandoffSourceInstruction{target: ref, alias: handoff.FromAlias}
}

func (w *stageWorkflow) handleHandoffSource(result handoffSourceResult) instructionSequence {
	if !w.matches(result.target) || w.active.phase != stageReadingHandoffSource {
		return nil
	}
	if !result.ok {
		return w.failHandoffSource()
	}
	state := w.active
	handoff := state.statement.(promptlang.Handoff)
	state.phase = stageDispatching
	state.dispatchRouting = activeAliases(state.routing, state.requirements.unavailable)
	ref := w.nextRef()
	state.pending = ref
	return instructionSequence{executeSessionInstruction{
		target: ref,
		request: handoffRequest{
			fromAlias: handoff.FromAlias, toAlias: handoff.ToAlias,
			requiredReadyAliases: activeRequiredReadyAliases(state.requirements.participants, state.requirements.unavailable),
			source:               result.source,
		},
		recordsOnSuccess: []room.Record{{
			Kind: room.KindUserInput, Text: state.raw,
			Routing: slices.Clone(state.dispatchRouting),
		}},
	}}
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

func (w *stageWorkflow) captureHandoffCompletion(event session.Event) {
	statement, isHandoff := w.active.statement.(promptlang.Handoff)
	handoff, completed := event.(session.HandoffDelivered)
	if !isHandoff || !completed ||
		handoff.FromAlias != statement.FromAlias || handoff.ToAlias != statement.ToAlias {
		return
	}
	handoffCopy := handoff
	w.active.handoff.completed = &handoffCopy
}

func (w *stageWorkflow) handleHandoffSessionEvent(event session.Event) instructionSequence {
	if !w.applyHandoffSessionEvent(event) {
		return nil
	}
	return w.advanceWaitingHandoff()
}

func (w *stageWorkflow) applyHandoffSessionEvent(event session.Event) bool {
	handoff := w.active.statement.(promptlang.Handoff)
	switch event := event.(type) {
	case session.ParticipantStatusChanged:
		w.applyHandoffStatus(event, handoff)
	case session.AgentMessage:
		return w.applyHandoffMessage(event, handoff)
	case session.AgentReady:
		w.active.requirements.markReady(event.Alias)
	case session.AgentStopped:
		w.active.requirements.markUnavailable(event.Alias)
	case session.AgentCrashed:
		w.active.requirements.markUnavailable(event.Alias)
	default:
		return false
	}
	return true
}

func (w *stageWorkflow) applyHandoffStatus(
	event session.ParticipantStatusChanged,
	handoff promptlang.Handoff,
) {
	w.active.requirements.updateStatus(event.Alias, event.To)
	if event.Alias == handoff.FromAlias && (participant.View{Status: event.To}).HasActiveTurn() {
		w.active.handoff.sourceNeedsCompletion = true
	}
}

func (w *stageWorkflow) applyHandoffMessage(
	event session.AgentMessage,
	handoff promptlang.Handoff,
) bool {
	if event.Alias != handoff.FromAlias || !event.TurnCompleted {
		return false
	}
	if expected := w.active.requirements.turnID(event.Alias); event.TurnID < expected {
		return false
	}
	w.active.handoff.sourceNeedsCompletion = false
	w.active.requirements.updateTurnID(event.Alias, event.TurnID)
	return true
}

func (w *stageWorkflow) advanceWaitingHandoff() instructionSequence {
	state := w.active
	if w.mustDiscard() {
		return w.discardUnavailableStage()
	}
	if !state.requirements.isReady() || state.handoff.sourceNeedsCompletion {
		return instructionSequence{requestSnapshotInstruction{}}
	}
	return instructionSequence{w.readHandoffSourceInstruction(), requestSnapshotInstruction{}}
}

func activeRequiredReadyAliases(readinessRequirements []participantState, unavailable []string) []string {
	aliases := make([]string, 0, len(readinessRequirements))
	for _, value := range readinessRequirements {
		if !slices.Contains(unavailable, value.alias) {
			aliases = append(aliases, value.alias)
		}
	}
	slices.Sort(aliases)
	return aliases
}

func handoffSourceIsWorking(state *stageState) bool {
	handoff, ok := state.statement.(promptlang.Handoff)
	if !ok {
		return false
	}
	for _, value := range state.requirements.participants {
		if value.alias == handoff.FromAlias {
			return value.view().HasActiveTurn()
		}
	}
	return false
}

func handoffRouting(statement promptlang.Handoff) []string {
	if statement.FromAlias == statement.ToAlias {
		return []string{statement.FromAlias}
	}
	return []string{statement.FromAlias, statement.ToAlias}
}
