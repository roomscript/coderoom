package interpreter

import (
	"errors"
	"fmt"
	"slices"

	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/promptlang"
	"github.com/roomscript/coderoom/internal/room"
)

var errNoStageTargets = errors.New("no participants available for staged submission")
var errStageTargetUnavailable = errors.New("no participant named")
var errStageTargetCrashed = errors.New("participant has crashed")
var errUnsupportedStageAction = errors.New("unsupported staged action")
var errNoHandoffSource = errors.New("handoff source has no completed room-visible output")

type stageState struct {
	source     promptlang.ParsedStatement
	generation uint64
	raw        string
	pending    workflowRef
	stagePlan
	phase             stagePhase
	dispatchRouting   []string
	submissionPending bool
	interruption      stageInterruption
	handoff           *handoffStage
}

type stagePhase uint8

const (
	stagePlanning stagePhase = iota
	stageWaiting
	stageReadingHandoffSource
	stageDispatching
)

type stageWorkflow struct {
	active         *stageState
	nextGeneration uint64
	nextRequestID  uint64
}

func (w *stageWorkflow) pending() bool { return w.active != nil }

func (w *stageWorkflow) mustDiscard() bool {
	state := w.active
	if state.send != nil {
		return slices.Contains(state.requirements.unavailable, state.send.action.Alias.Value)
	}
	if state.broadcast != nil {
		return len(activeAliases(state.routing, state.requirements.unavailable)) == 0
	}
	if state.handoff != nil {
		handoff := state.handoff.action
		return slices.Contains(state.requirements.unavailable, handoff.FromAlias.Value) ||
			slices.Contains(state.requirements.unavailable, handoff.ToAlias.Value)
	}
	return false
}

func (w *stageWorkflow) discardUnavailableStage() instructionSequence {
	state := w.active
	message := "staged submission discarded: no active targets"
	presentationMessage := "staged message discarded: no active targets"
	if state.send != nil &&
		len(activeAliases(state.routing, state.requirements.unavailable)) != 0 {
		message = fmt.Sprintf("staged submission discarded: %q is no longer available", state.send.action.Alias.Value)
		presentationMessage = fmt.Sprintf("staged message discarded: %q is no longer available", state.send.action.Alias.Value)
	} else if state.handoff != nil {
		handoff := state.handoff.action
		missing := handoff.FromAlias.Value
		if !slices.Contains(state.requirements.unavailable, missing) {
			missing = handoff.ToAlias.Value
		}
		message = fmt.Sprintf("staged submission discarded: %q is no longer available", missing)
		presentationMessage = fmt.Sprintf("staged message discarded: %q is no longer available", missing)
	}
	w.active = nil
	sequence := instructionSequence{
		appendRecordInstruction{record: room.Record{Kind: room.KindSystem, Text: message}},
	}
	sequence = append(sequence, publishEventInstruction{event: StagedInputDiscarded{
		Raw: state.raw, Reason: presentationMessage,
	}})
	return append(sequence, requestSnapshotInstruction{})
}

func (w *stageWorkflow) snapshot() *StagedSubmission {
	if w.active == nil || len(w.active.requirements.participants) == 0 && len(w.active.routing) == 0 {
		return nil
	}
	return &StagedSubmission{
		Raw: w.active.raw, Routing: slices.Clone(w.active.routing),
		NotReadyAliases:    w.active.requirements.waitingAliases(),
		Interruptible:      w.active.requirements.interruptibleAliases(w.active.interruption.cancelled),
		Unavailable:        slices.Clone(w.active.requirements.unavailable),
		InterruptRequested: w.active.interruption.requested,
		Phase:              StagePhasePending,
	}
}

func (w *stageWorkflow) matches(ref workflowRef) bool {
	return w.active != nil && w.active.pending == ref && ref.kind == workflowStage
}

func (w *stageWorkflow) nextRef() workflowRef {
	w.nextRequestID++
	return workflowRef{kind: workflowStage, generation: w.active.generation, requestID: w.nextRequestID}
}

func acceptedStageInputSequence(raw string, routing []string) instructionSequence {
	return instructionSequence{
		publishEventInstruction{event: InputAccepted{Raw: raw, Routing: slices.Clone(routing)}},
	}
}

func activeAliases(routing, unavailable []string) []string {
	active := make([]string, 0, len(routing))
	for _, alias := range routing {
		if !slices.Contains(unavailable, alias) {
			active = append(active, alias)
		}
	}
	return active
}

func unavailableStageFailure(state *stageState) SubmissionFailed {
	failure := SubmissionFailed{Raw: state.raw, Operation: "staged dispatch", Code: ErrorExecutionFailed, Err: errNoStageTargets}
	if state.send == nil {
		return failure
	}
	failure.Code = ErrorParticipantUnavailable
	failure.Err = fmt.Errorf("%w %q", errStageTargetUnavailable, state.send.action.Alias.Value)
	for _, value := range state.requirements.participants {
		if value.alias == state.send.action.Alias.Value && value.status == participant.StatusCrashed {
			failure.Err = fmt.Errorf("%w: %q", errStageTargetCrashed, state.send.action.Alias.Value)
			return failure
		}
	}
	return failure
}
