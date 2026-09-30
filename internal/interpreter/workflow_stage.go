package interpreter

import (
	"errors"
	"fmt"
	"slices"

	"github.com/trigosec/coderoom/internal/participant"
	"github.com/trigosec/coderoom/internal/promptlang"
	"github.com/trigosec/coderoom/internal/room"
	"github.com/trigosec/coderoom/internal/session"
)

var errNoStageTargets = errors.New("no participants available for staged submission")

type stageState struct {
	generation  uint64
	raw         string
	statement   promptlang.Statement
	pending     workflowRef
	plan        session.SharedSendPlan
	broadcast   []string
	routing     []string
	barrier     []participantState
	blocking    []string
	unavailable []string
}

type stageWorkflow struct {
	active         *stageState
	nextGeneration uint64
	nextRequestID  uint64
}

func (w *stageWorkflow) pending() bool { return w.active != nil }

func (w *stageWorkflow) start(raw string, statement promptlang.Statement) instructionSequence {
	w.nextGeneration++
	w.active = &stageState{
		generation: w.nextGeneration,
		raw:        raw,
		statement:  statement,
	}
	ref := w.nextRef()
	w.active.pending = ref
	if send, ok := statement.(promptlang.Send); ok {
		return instructionSequence{planSharedSendInstruction{target: ref, alias: send.Alias}}
	}
	if _, ok := statement.(promptlang.Broadcast); ok {
		return instructionSequence{planBroadcastInstruction{target: ref}}
	}
	return instructionSequence{readParticipantStateInstruction{target: ref}}
}

func (w *stageWorkflow) handleCompletion(completion workflowCompletion) instructionSequence {
	switch completion := completion.(type) {
	case sharedSendPlanResult:
		return w.handleSharedSendPlan(completion)
	case broadcastPlanResult:
		return w.handleBroadcastPlan(completion)
	case participantStateResult:
		return w.handleParticipantState(completion)
	case sessionCompletion:
		return w.handleSessionCompletion(completion)
	default:
		return nil
	}
}

func (w *stageWorkflow) handleBroadcastPlan(result broadcastPlanResult) instructionSequence {
	if !w.matches(result.target) {
		return nil
	}
	w.active.broadcast = slices.Clone(result.targets)
	w.active.routing = slices.Clone(result.targets)
	ref := w.nextRef()
	w.active.pending = ref
	return instructionSequence{readParticipantStateInstruction{target: ref}}
}

func (w *stageWorkflow) handleSharedSendPlan(result sharedSendPlanResult) instructionSequence {
	if !w.matches(result.target) {
		return nil
	}
	w.active.plan = result.plan
	w.active.routing = slices.Clone(result.targets)
	ref := w.nextRef()
	w.active.pending = ref
	return instructionSequence{readParticipantStateInstruction{target: ref}}
}

func (w *stageWorkflow) handleParticipantState(result participantStateResult) instructionSequence {
	if !w.matches(result.target) {
		return nil
	}
	state := w.active
	state.barrier = freezeStageBarrier(state, result.barrier)
	if handoff, ok := state.statement.(promptlang.Handoff); ok {
		state.routing = handoffRouting(handoff)
	}
	state.blocking, state.unavailable = stageReadiness(state.barrier, state.routing)
	sequence := acceptedStageInputSequence(state.raw, state.routing)
	if len(state.routing) == 0 {
		raw := state.raw
		w.active = nil
		return append(sequence, publishEventInstruction{event: SubmissionFailed{
			Raw: raw, Operation: "staged dispatch", Code: ErrorExecutionFailed,
			Err: errNoStageTargets,
		}})
	}
	if w.readyToDispatch() {
		return append(sequence, w.dispatchInstruction())
	}
	sequence = append(sequence,
		requestSnapshotInstruction{},
		publishEventInstruction{event: SubmissionSucceeded{Raw: state.raw}},
	)
	return sequence
}

func (w *stageWorkflow) readyToDispatch() bool {
	if w.active == nil || len(w.active.routing) == 0 ||
		len(w.active.blocking) != 0 || len(w.active.unavailable) != 0 {
		return false
	}
	_, handoff := w.active.statement.(promptlang.Handoff)
	return !handoff
}

func (w *stageWorkflow) dispatchInstruction() executeSessionInstruction {
	state := w.active
	ref := w.nextRef()
	state.pending = ref
	var request sessionRequest
	switch statement := state.statement.(type) {
	case promptlang.Send:
		request = executePlannedSharedSendRequest{
			plan: state.plan, directText: statement.Text,
			listenersText: fmt.Sprintf("@%s: %s", statement.Alias, statement.Text),
		}
	case promptlang.Broadcast:
		request = broadcastRequest{aliases: slices.Clone(state.broadcast), text: statement.Text}
	default:
		panic(fmt.Sprintf("unsupported immediate stage dispatch %T", state.statement))
	}
	return executeSessionInstruction{target: ref, request: request}
}

func (w *stageWorkflow) handleSessionCompletion(completion sessionCompletion) instructionSequence {
	if !w.matches(completion.target) {
		return nil
	}
	raw := w.active.raw
	w.active = nil
	sequence := instructionSequence{requestSnapshotInstruction{}}
	if completion.err != nil {
		return append(sequence, publishEventInstruction{event: SubmissionFailed{
			Raw: raw, Operation: "staged dispatch", Code: ErrorExecutionFailed, Err: completion.err,
		}})
	}
	return append(sequence, publishEventInstruction{event: SubmissionSucceeded{Raw: raw}})
}

func (w *stageWorkflow) snapshot() *StagedSubmission {
	if w.active == nil || len(w.active.barrier) == 0 && len(w.active.routing) == 0 {
		return nil
	}
	return &StagedSubmission{
		Raw: w.active.raw, Routing: slices.Clone(w.active.routing),
		Blocking:    slices.Clone(w.active.blocking),
		Unavailable: slices.Clone(w.active.unavailable), Phase: StagePhasePending,
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
		appendRecordInstruction{record: room.Record{
			Kind: room.KindUserInput, Text: raw, Routing: slices.Clone(routing),
		}},
		publishEventInstruction{event: InputAccepted{Raw: raw, Routing: slices.Clone(routing)}},
	}
}

func freezeStageBarrier(state *stageState, participants []participantState) []participantState {
	switch state.statement.(type) {
	case promptlang.Send, promptlang.Broadcast:
	default:
		return slices.Clone(participants)
	}
	byAlias := make(map[string]participantState, len(participants))
	for _, value := range participants {
		byAlias[value.alias] = value
	}
	barrier := make([]participantState, 0, len(state.routing))
	for _, alias := range state.routing {
		if value, ok := byAlias[alias]; ok {
			barrier = append(barrier, value)
		}
	}
	return barrier
}

func stageReadiness(barrier []participantState, routing []string) ([]string, []string) {
	byAlias := make(map[string]participant.Status, len(barrier))
	var blocking []string
	for _, value := range barrier {
		byAlias[value.alias] = value.status
		if value.status != participant.StatusIdle {
			blocking = append(blocking, value.alias)
		}
	}
	var unavailable []string
	for _, alias := range routing {
		_, ok := byAlias[alias]
		if !ok {
			unavailable = append(unavailable, alias)
		}
	}
	slices.Sort(blocking)
	slices.Sort(unavailable)
	return blocking, unavailable
}

func handoffRouting(statement promptlang.Handoff) []string {
	if statement.FromAlias == statement.ToAlias {
		return []string{statement.FromAlias}
	}
	return []string{statement.FromAlias, statement.ToAlias}
}
