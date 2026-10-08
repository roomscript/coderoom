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
	state.dispatchRouting = activeAliases(state.routing, state.unavailable)
	ref := w.nextRef()
	state.pending = ref
	return instructionSequence{executeSessionInstruction{
		target: ref,
		request: handoffRequest{
			fromAlias: handoff.FromAlias, toAlias: handoff.ToAlias,
			requiredReadyAliases: activeRequiredReadyAliases(state.readinessRequirements, state.unavailable),
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
		w.markStarted(event.Alias)
	case session.AgentStopped:
		w.markUnavailable(event.Alias)
	case session.AgentCrashed:
		w.markUnavailable(event.Alias)
	default:
		return false
	}
	return true
}

func (w *stageWorkflow) applyHandoffStatus(
	event session.ParticipantStatusChanged,
	handoff promptlang.Handoff,
) {
	w.updateRequiredStatus(event.Alias, event.To)
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
	if expected := requiredTurnID(w.active.readinessRequirements, event.Alias); event.TurnID < expected {
		return false
	}
	w.active.handoff.sourceNeedsCompletion = false
	w.updateRequiredTurn(event.Alias, event.TurnID)
	return true
}

func requiredTurnID(readinessRequirements []participantState, alias string) uint64 {
	for _, value := range readinessRequirements {
		if value.alias == alias {
			return value.turnID
		}
	}
	return 0
}

func (w *stageWorkflow) updateRequiredTurn(alias string, turnID uint64) {
	for index := range w.active.readinessRequirements {
		if w.active.readinessRequirements[index].alias == alias {
			w.active.readinessRequirements[index].turnID = turnID
			return
		}
	}
}

func (w *stageWorkflow) advanceWaitingHandoff() instructionSequence {
	state := w.active
	state.notReadyAliases = notReadyAliases(state.readinessRequirements, state.unavailable)
	if w.mustDiscard() {
		return w.discardUnavailableStage()
	}
	if len(state.notReadyAliases) != 0 || state.handoff.sourceNeedsCompletion {
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
	for _, value := range state.readinessRequirements {
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
