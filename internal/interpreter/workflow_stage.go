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
var errStageTargetUnavailable = errors.New("staged submission target unavailable")

type stageState struct {
	generation        uint64
	raw               string
	statement         promptlang.Statement
	pending           workflowRef
	plan              session.SharedSendPlan
	routing           []string
	barrier           []participantState
	blocking          []string
	unavailable       []string
	phase             stagePhase
	dispatchRouting   []string
	submissionPending bool
}

type stagePhase uint8

const (
	stagePlanning stagePhase = iota
	stageWaiting
	stageDispatching
)

type stageWorkflow struct {
	active         *stageState
	nextGeneration uint64
	nextRequestID  uint64
}

func (w *stageWorkflow) pending() bool { return w.active != nil }

func (w *stageWorkflow) start(raw string, statement promptlang.Statement) instructionSequence {
	w.nextGeneration++
	w.active = &stageState{
		generation:        w.nextGeneration,
		raw:               raw,
		statement:         statement,
		phase:             stagePlanning,
		submissionPending: true,
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
	if w.mustDiscard() {
		err := errNoStageTargets
		if send, ok := state.statement.(promptlang.Send); ok {
			err = fmt.Errorf("%w: %s", errStageTargetUnavailable, send.Alias)
		}
		raw := state.raw
		w.active = nil
		return append(sequence, publishEventInstruction{event: SubmissionFailed{
			Raw: raw, Operation: "staged dispatch", Code: ErrorExecutionFailed, Err: err,
		}})
	}
	if w.readyToDispatch() {
		return append(sequence, w.dispatchInstruction())
	}
	state.phase = stageWaiting
	state.submissionPending = false
	sequence = append(sequence,
		requestSnapshotInstruction{},
		publishEventInstruction{event: SubmissionSucceeded{Raw: state.raw}},
	)
	return sequence
}

func (w *stageWorkflow) readyToDispatch() bool {
	if w.active == nil || len(w.active.routing) == 0 ||
		len(w.active.blocking) != 0 {
		return false
	}
	_, handoff := w.active.statement.(promptlang.Handoff)
	return !handoff
}

func (w *stageWorkflow) dispatchInstruction() executeSessionInstruction {
	state := w.active
	state.phase = stageDispatching
	ref := w.nextRef()
	state.pending = ref
	var request sessionRequest
	switch statement := state.statement.(type) {
	case promptlang.Send:
		state.dispatchRouting = activeAliases(state.routing, state.unavailable)
		request = executePlannedSharedSendRequest{
			plan: state.plan.DiscardUnavailableListeners(state.unavailable), directText: statement.Text,
			listenersText: fmt.Sprintf("@%s: %s", statement.Alias, statement.Text),
		}
	case promptlang.Broadcast:
		state.dispatchRouting = activeAliases(state.routing, state.unavailable)
		request = broadcastRequest{aliases: slices.Clone(state.dispatchRouting), text: statement.Text}
	default:
		panic(fmt.Sprintf("unsupported immediate stage dispatch %T", state.statement))
	}
	return executeSessionInstruction{target: ref, request: request}
}

func (w *stageWorkflow) handleSessionCompletion(completion sessionCompletion) instructionSequence {
	if !w.matches(completion.target) {
		return nil
	}
	state := w.active
	w.active = nil
	sequence := instructionSequence{requestSnapshotInstruction{}}
	delivered := slices.Clone(state.dispatchRouting)
	if completion.err != nil {
		delivered = session.DeliveredAliases(completion.err)
	}
	if len(delivered) != 0 {
		sequence = append(sequence, appendRecordInstruction{record: room.Record{
			Kind: room.KindUserInput, Text: state.raw, Routing: slices.Clone(delivered),
		}})
	}
	if completion.err != nil {
		if !state.submissionPending {
			return append(sequence, publishEventInstruction{event: OperationFailed{
				Operation: "staged dispatch", Err: completion.err,
			}})
		}
		return append(sequence, publishEventInstruction{event: SubmissionFailed{
			Raw: state.raw, Operation: "staged dispatch",
			Code: ErrorExecutionFailed, Err: completion.err,
		}})
	}
	if state.submissionPending {
		sequence = append(sequence, publishEventInstruction{event: SubmissionSucceeded{Raw: state.raw}})
	}
	return sequence
}

func (w *stageWorkflow) handleSessionEvent(event session.Event) instructionSequence {
	if w.active == nil || w.active.phase != stageWaiting {
		return nil
	}
	if _, handoff := w.active.statement.(promptlang.Handoff); handoff {
		return nil
	}
	switch event := event.(type) {
	case session.ParticipantStatusChanged:
		w.updateBarrierStatus(event.Alias, event.To)
	case session.AgentStarted:
		w.updateBarrierStatus(event.Alias, participant.StatusIdle)
	case session.AgentStopped:
		w.markUnavailable(event.Alias)
	case session.AgentCrashed:
		w.markUnavailable(event.Alias)
	default:
		return nil
	}
	return w.advanceWaitingStage()
}

func (w *stageWorkflow) updateBarrierStatus(alias string, status participant.Status) {
	if slices.Contains(w.active.unavailable, alias) {
		return
	}
	for index := range w.active.barrier {
		if w.active.barrier[index].alias == alias {
			w.active.barrier[index].status = status
			return
		}
	}
}

func (w *stageWorkflow) markUnavailable(alias string) {
	if !containsParticipant(w.active.barrier, alias) || slices.Contains(w.active.unavailable, alias) {
		return
	}
	w.active.unavailable = append(w.active.unavailable, alias)
	slices.Sort(w.active.unavailable)
}

func (w *stageWorkflow) advanceWaitingStage() instructionSequence {
	state := w.active
	state.blocking = blockedAliases(state.barrier, state.unavailable)
	if w.mustDiscard() {
		return w.discardUnavailableStage()
	}
	if len(state.blocking) != 0 {
		return instructionSequence{requestSnapshotInstruction{}}
	}
	return instructionSequence{w.dispatchInstruction(), requestSnapshotInstruction{}}
}

func (w *stageWorkflow) mustDiscard() bool {
	state := w.active
	if send, ok := state.statement.(promptlang.Send); ok {
		return slices.Contains(state.unavailable, send.Alias)
	}
	_, broadcast := state.statement.(promptlang.Broadcast)
	return broadcast && len(activeAliases(state.routing, state.unavailable)) == 0
}

func (w *stageWorkflow) discardUnavailableStage() instructionSequence {
	state := w.active
	message := "staged submission discarded: no active targets"
	if send, ok := state.statement.(promptlang.Send); ok {
		message = fmt.Sprintf("staged submission discarded: %q is no longer available", send.Alias)
	}
	w.active = nil
	return instructionSequence{
		appendRecordInstruction{record: room.Record{Kind: room.KindSystem, Text: message}},
		requestSnapshotInstruction{},
	}
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
	for _, value := range barrier {
		byAlias[value.alias] = value.status
	}
	var unavailable []string
	for _, alias := range routing {
		_, ok := byAlias[alias]
		if !ok {
			unavailable = append(unavailable, alias)
		}
	}
	slices.Sort(unavailable)
	return blockedAliases(barrier, unavailable), unavailable
}

func blockedAliases(barrier []participantState, unavailable []string) []string {
	var blocked []string
	for _, value := range barrier {
		if value.status != participant.StatusIdle && !slices.Contains(unavailable, value.alias) {
			blocked = append(blocked, value.alias)
		}
	}
	slices.Sort(blocked)
	return blocked
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

func containsParticipant(participants []participantState, alias string) bool {
	return slices.ContainsFunc(participants, func(value participantState) bool {
		return value.alias == alias
	})
}

func handoffRouting(statement promptlang.Handoff) []string {
	if statement.FromAlias == statement.ToAlias {
		return []string{statement.FromAlias}
	}
	return []string{statement.FromAlias, statement.ToAlias}
}
