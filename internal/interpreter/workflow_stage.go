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
var errNoHandoffSource = errors.New("handoff source has no completed room-visible output")

type stageState struct {
	generation            uint64
	raw                   string
	statement             promptlang.Statement
	pending               workflowRef
	plan                  session.SharedSendPlan
	routing               []string
	barrier               []participantState
	blocking              []string
	unavailable           []string
	phase                 stagePhase
	dispatchRouting       []string
	submissionPending     bool
	sourceNeedsCompletion bool
	interruptRequested    bool
	interruptPending      map[workflowRef]string
	interrupted           []string
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

func (w *stageWorkflow) takeForEdit() (instructionSequence, string, bool) {
	if w.active == nil {
		return nil, "", false
	}
	raw := w.active.raw
	w.active = nil
	return instructionSequence{requestSnapshotInstruction{}}, raw, true
}

func (w *stageWorkflow) discard() (instructionSequence, bool) {
	if w.active == nil {
		return nil, false
	}
	w.active = nil
	return instructionSequence{requestSnapshotInstruction{}}, true
}

func (w *stageWorkflow) interruptAndDispatch() (instructionSequence, bool) {
	if w.active == nil || len(w.active.interruptPending) != 0 {
		return nil, false
	}
	state := w.active
	state.blocking = blockedAliases(state.barrier, state.unavailable)
	if len(state.blocking) == 0 {
		if state.interruptRequested {
			return nil, false
		}
		state.interruptRequested = true
		return w.advanceInterruptedStage(), true
	}
	aliases := activeAliases(state.blocking, state.interrupted)
	if len(aliases) == 0 {
		return nil, false
	}
	state.interruptRequested = true
	state.interruptPending = make(map[workflowRef]string, len(aliases))
	sequence := make(instructionSequence, 0, len(aliases)+1)
	for _, alias := range aliases {
		ref := w.nextRef()
		state.interruptPending[ref] = alias
		sequence = append(sequence, executeSessionInstruction{
			target: ref, request: cancelRequest{alias: alias},
			recordsOnSuccess: []room.Record{{
				Kind: room.KindSystem, Text: fmt.Sprintf("[→ %s] interrupt requested", alias),
			}},
		})
	}
	return append(sequence, requestSnapshotInstruction{}), true
}

func (w *stageWorkflow) advanceInterruptedStage() instructionSequence {
	if _, handoff := w.active.statement.(promptlang.Handoff); handoff {
		if w.active.sourceNeedsCompletion {
			return instructionSequence{requestSnapshotInstruction{}}
		}
		return instructionSequence{w.readHandoffSourceInstruction(), requestSnapshotInstruction{}}
	}
	return instructionSequence{w.dispatchInstruction(), requestSnapshotInstruction{}}
}

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
	case handoffSourceResult:
		return w.handleHandoffSource(completion)
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
	state.sourceNeedsCompletion = handoffSourceIsWorking(state)
	sequence := acceptedStageInputSequence(state.raw, state.routing)
	return w.completeParticipantPlanning(sequence)
}

func (w *stageWorkflow) completeParticipantPlanning(sequence instructionSequence) instructionSequence {
	state := w.active
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
	if _, handoff := state.statement.(promptlang.Handoff); handoff {
		if len(state.blocking) == 0 && !state.sourceNeedsCompletion {
			return append(sequence, w.readHandoffSourceInstruction())
		}
		state.phase = stageWaiting
		state.submissionPending = false
		return append(sequence,
			requestSnapshotInstruction{},
			publishEventInstruction{event: SubmissionSucceeded{Raw: state.raw}},
		)
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
			idleAliases: activeBarrierAliases(state.barrier, state.unavailable),
			source:      result.source,
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
	if sequence, handled := w.handlePendingInterruptCompletion(completion); handled {
		return sequence
	}
	if !w.matches(completion.target) {
		return nil
	}
	state := w.active
	w.active = nil
	sequence := stageDispatchRecordSequence(state, completion)
	return append(sequence, stageDispatchOutcomeInstruction(state, completion)...)
}

func (w *stageWorkflow) handlePendingInterruptCompletion(
	completion sessionCompletion,
) (instructionSequence, bool) {
	if w.active == nil {
		return nil, false
	}
	alias, ok := w.active.interruptPending[completion.target]
	if !ok {
		return nil, false
	}
	return w.handleInterruptCompletion(completion, alias), true
}

func stageDispatchRecordSequence(
	state *stageState,
	completion sessionCompletion,
) instructionSequence {
	sequence := instructionSequence{requestSnapshotInstruction{}}
	delivered := slices.Clone(state.dispatchRouting)
	if completion.err != nil {
		delivered = session.DeliveredAliases(completion.err)
	}
	if len(delivered) != 0 && !completion.successRecordsApplied {
		sequence = append(sequence, appendRecordInstruction{record: room.Record{
			Kind: room.KindUserInput, Text: state.raw, Routing: slices.Clone(delivered),
		}})
	}
	if _, send := state.statement.(promptlang.Send); send && len(delivered) != 0 {
		sequence = append(sequence, publishEventInstruction{event: StagedInputDispatched{
			Raw: state.raw, Routing: slices.Clone(delivered),
		}})
	}
	return sequence
}

func stageDispatchOutcomeInstruction(
	state *stageState,
	completion sessionCompletion,
) instructionSequence {
	if completion.err != nil {
		if !state.submissionPending {
			return instructionSequence{publishEventInstruction{event: OperationFailed{
				Operation: "staged dispatch", Err: completion.err,
			}}}
		}
		return instructionSequence{publishEventInstruction{event: SubmissionFailed{
			Raw: state.raw, Operation: "staged dispatch",
			Code: ErrorExecutionFailed, Err: completion.err,
		}}}
	}
	if state.submissionPending {
		return instructionSequence{publishEventInstruction{event: SubmissionSucceeded{Raw: state.raw}}}
	}
	return nil
}

func (w *stageWorkflow) handleInterruptCompletion(
	completion sessionCompletion,
	alias string,
) instructionSequence {
	delete(w.active.interruptPending, completion.target)
	sequence := instructionSequence{requestSnapshotInstruction{}}
	if completion.err == nil {
		w.active.interrupted = append(w.active.interrupted, alias)
		slices.Sort(w.active.interrupted)
		return sequence
	}
	return append(sequence, publishEventInstruction{event: OperationFailed{
		Operation: "interrupt staged submission", Err: completion.err,
	}})
}

func (w *stageWorkflow) handleSessionEvent(event session.Event) instructionSequence {
	if w.active == nil || w.active.phase != stageWaiting {
		return nil
	}
	if _, handoff := w.active.statement.(promptlang.Handoff); handoff {
		return w.handleHandoffSessionEvent(event)
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
	case session.AgentStarted:
		w.updateBarrierStatus(event.Alias, participant.StatusIdle)
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
	w.updateBarrierStatus(event.Alias, event.To)
	if event.Alias == handoff.FromAlias && event.To != participant.StatusIdle {
		w.active.sourceNeedsCompletion = true
	}
}

func (w *stageWorkflow) applyHandoffMessage(
	event session.AgentMessage,
	handoff promptlang.Handoff,
) bool {
	if event.Alias != handoff.FromAlias || !event.TurnCompleted {
		return false
	}
	if expected := barrierTurnID(w.active.barrier, event.Alias); event.TurnID < expected {
		return false
	}
	w.active.sourceNeedsCompletion = false
	w.updateBarrierTurn(event.Alias, event.TurnID)
	return true
}

func barrierTurnID(barrier []participantState, alias string) uint64 {
	for _, value := range barrier {
		if value.alias == alias {
			return value.turnID
		}
	}
	return 0
}

func (w *stageWorkflow) updateBarrierTurn(alias string, turnID uint64) {
	for index := range w.active.barrier {
		if w.active.barrier[index].alias == alias {
			w.active.barrier[index].turnID = turnID
			return
		}
	}
}

func (w *stageWorkflow) advanceWaitingHandoff() instructionSequence {
	state := w.active
	state.blocking = blockedAliases(state.barrier, state.unavailable)
	if w.mustDiscard() {
		return w.discardUnavailableStage()
	}
	if len(state.blocking) != 0 || state.sourceNeedsCompletion {
		return instructionSequence{requestSnapshotInstruction{}}
	}
	return instructionSequence{w.readHandoffSourceInstruction(), requestSnapshotInstruction{}}
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
	if broadcast {
		return len(activeAliases(state.routing, state.unavailable)) == 0
	}
	if handoff, ok := state.statement.(promptlang.Handoff); ok {
		return slices.Contains(state.unavailable, handoff.FromAlias) ||
			slices.Contains(state.unavailable, handoff.ToAlias)
	}
	return false
}

func (w *stageWorkflow) discardUnavailableStage() instructionSequence {
	state := w.active
	message := "staged submission discarded: no active targets"
	presentationMessage := "staged message discarded: no active targets"
	if send, ok := state.statement.(promptlang.Send); ok &&
		len(activeAliases(state.routing, state.unavailable)) != 0 {
		message = fmt.Sprintf("staged submission discarded: %q is no longer available", send.Alias)
		presentationMessage = fmt.Sprintf("staged message discarded: %q is no longer available", send.Alias)
	} else if handoff, ok := state.statement.(promptlang.Handoff); ok {
		missing := handoff.FromAlias
		if !slices.Contains(state.unavailable, missing) {
			missing = handoff.ToAlias
		}
		message = fmt.Sprintf("staged submission discarded: %q is no longer available", missing)
	}
	w.active = nil
	sequence := instructionSequence{
		appendRecordInstruction{record: room.Record{Kind: room.KindSystem, Text: message}},
	}
	if _, send := state.statement.(promptlang.Send); send {
		sequence = append(sequence, publishEventInstruction{event: StagedInputDiscarded{
			Raw: state.raw, Reason: presentationMessage,
		}})
	}
	return append(sequence, requestSnapshotInstruction{})
}

func (w *stageWorkflow) snapshot() *StagedSubmission {
	if w.active == nil || len(w.active.barrier) == 0 && len(w.active.routing) == 0 {
		return nil
	}
	return &StagedSubmission{
		Raw: w.active.raw, Routing: slices.Clone(w.active.routing),
		Blocking:           slices.Clone(w.active.blocking),
		Unavailable:        slices.Clone(w.active.unavailable),
		InterruptRequested: w.active.interruptRequested,
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

func activeBarrierAliases(barrier []participantState, unavailable []string) []string {
	aliases := make([]string, 0, len(barrier))
	for _, value := range barrier {
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
	for _, value := range state.barrier {
		if value.alias == handoff.FromAlias {
			return value.status != participant.StatusIdle
		}
	}
	return false
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
