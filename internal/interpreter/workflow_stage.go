package interpreter

import (
	"errors"
	"fmt"
	"slices"

	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/promptlang"
	"github.com/roomscript/coderoom/internal/room"
	"github.com/roomscript/coderoom/internal/session"
)

var errNoStageTargets = errors.New("no participants available for staged submission")
var errStageTargetUnavailable = errors.New("no participant named")
var errStageTargetCrashed = errors.New("participant has crashed")
var errNoHandoffSource = errors.New("handoff source has no completed room-visible output")

type stageState struct {
	generation            uint64
	raw                   string
	statement             promptlang.Statement
	pending               workflowRef
	plan                  session.ParticipantSendPlan
	routing               []string
	readinessRequirements []participantState
	notReadyAliases       []string
	unavailable           []string
	phase                 stagePhase
	dispatchRouting       []string
	submissionPending     bool
	sourceNeedsCompletion bool
	interruptRequested    bool
	interruptPending      map[workflowRef]string
	interrupted           []string
	handoffCompleted      *session.HandoffDelivered
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
	state.notReadyAliases = notReadyAliases(state.readinessRequirements, state.unavailable)
	if len(state.notReadyAliases) == 0 {
		if state.interruptRequested {
			return nil, false
		}
		state.interruptRequested = true
		return w.advanceInterruptedStage(), true
	}
	aliases := interruptibleStageAliases(state)
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
		return instructionSequence{prepareSendInstruction{target: ref, alias: send.Alias}}
	}
	if _, ok := statement.(promptlang.Broadcast); ok {
		return instructionSequence{planBroadcastInstruction{target: ref}}
	}
	return instructionSequence{readParticipantStateInstruction{target: ref}}
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

func (w *stageWorkflow) handleParticipantState(result participantStateResult) instructionSequence {
	if !w.matches(result.target) {
		return nil
	}
	if _, send := w.active.statement.(promptlang.Send); send {
		return nil // Sends obtain readiness during synchronous preparation.
	}
	state := w.active
	state.readinessRequirements = freezeReadinessRequirements(state, result.readinessRequirements)
	if handoff, ok := state.statement.(promptlang.Handoff); ok {
		state.routing = handoffRouting(handoff)
	}
	state.notReadyAliases, state.unavailable = stageReadiness(state.readinessRequirements, state.routing)
	state.sourceNeedsCompletion = handoffSourceIsWorking(state)
	return w.completeParticipantPlanning(nil)
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
		failure := unavailableStageFailure(state)
		w.active = nil
		return append(sequence, publishEventInstruction{event: failure})
	}
	sequence = append(sequence, acceptedStageInputSequence(state.raw, state.routing)...)

	if _, handoff := state.statement.(promptlang.Handoff); handoff {
		if len(state.notReadyAliases) == 0 && !state.sourceNeedsCompletion {
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
	return append(sequence, w.retainPlanUntilReady()...)
}

// retainPlanUntilReady is the suspension point. After publishing queued success,
// the stage waits for later lifecycle/output events to resume work.
func (w *stageWorkflow) retainPlanUntilReady() instructionSequence {
	w.active.phase = stageWaiting
	w.active.submissionPending = false
	return instructionSequence{
		requestSnapshotInstruction{},
		publishEventInstruction{event: SubmissionSucceeded{Raw: w.active.raw}},
	}
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

func (w *stageWorkflow) readyToDispatch() bool {
	if w.active == nil || len(w.active.routing) == 0 ||
		len(w.active.notReadyAliases) != 0 {
		return false
	}
	_, handoff := w.active.statement.(promptlang.Handoff)
	return !handoff
}

func (w *stageWorkflow) dispatchInstruction() executeSessionInstruction {
	if send, ok := w.active.statement.(promptlang.Send); ok {
		return w.startSendDispatch(send)
	}
	state := w.active
	state.phase = stageDispatching
	ref := w.nextRef()
	state.pending = ref
	var request sessionRequest
	switch statement := state.statement.(type) {
	case promptlang.Broadcast:
		state.dispatchRouting = activeAliases(state.routing, state.unavailable)
		request = broadcastRequest{aliases: slices.Clone(state.dispatchRouting), text: statement.Text}
	default:
		// Only sends and broadcasts use immediate dispatch; handoffs must resolve
		// their source first. The addressed-send path is typed above.
		panic(fmt.Sprintf("unsupported immediate stage dispatch %T", state.statement))
	}
	return executeSessionInstruction{
		target: ref, request: request,
		recordsOnSuccess: []room.Record{{Kind: room.KindUserInput, Text: state.raw}},
	}
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
	if w.active == nil {
		return nil
	}
	if w.active.phase == stageDispatching {
		w.captureHandoffCompletion(event)
		return nil
	}
	if w.active.phase != stageWaiting {
		return nil
	}
	if _, handoff := w.active.statement.(promptlang.Handoff); handoff {
		return w.handleHandoffSessionEvent(event)
	}
	switch event := event.(type) {
	case session.ParticipantStatusChanged:
		w.updateRequiredStatus(event.Alias, event.To)
	case session.AgentReady:
		w.markStarted(event.Alias)
	case session.AgentStopped:
		w.markUnavailable(event.Alias)
	case session.AgentCrashed:
		w.markUnavailable(event.Alias)
	default:
		return nil
	}
	return w.advanceWaitingStage()
}

func (w *stageWorkflow) captureHandoffCompletion(event session.Event) {
	statement, isHandoff := w.active.statement.(promptlang.Handoff)
	handoff, completed := event.(session.HandoffDelivered)
	if !isHandoff || !completed ||
		handoff.FromAlias != statement.FromAlias || handoff.ToAlias != statement.ToAlias {
		return
	}
	handoffCopy := handoff
	w.active.handoffCompleted = &handoffCopy
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
	if expected := requiredTurnID(w.active.readinessRequirements, event.Alias); event.TurnID < expected {
		return false
	}
	w.active.sourceNeedsCompletion = false
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
	if len(state.notReadyAliases) != 0 || state.sourceNeedsCompletion {
		return instructionSequence{requestSnapshotInstruction{}}
	}
	return instructionSequence{w.readHandoffSourceInstruction(), requestSnapshotInstruction{}}
}

func (w *stageWorkflow) updateRequiredStatus(alias string, status participant.Status) {
	if slices.Contains(w.active.unavailable, alias) {
		return
	}
	for index := range w.active.readinessRequirements {
		if w.active.readinessRequirements[index].alias == alias {
			if status == participant.StatusIdle && (w.active.readinessRequirements[index].status == participant.StatusStarting || w.active.readinessRequirements[index].status == participant.StatusAttached) {
				w.active.readinessRequirements[index].startupPending = true
			}
			w.active.readinessRequirements[index].status = status
			return
		}
	}
}

func (w *stageWorkflow) markUnavailable(alias string) {
	if !containsParticipant(w.active.readinessRequirements, alias) || slices.Contains(w.active.unavailable, alias) {
		return
	}
	w.active.unavailable = append(w.active.unavailable, alias)
	slices.Sort(w.active.unavailable)
}

func (w *stageWorkflow) advanceWaitingStage() instructionSequence {
	if send, ok := w.active.statement.(promptlang.Send); ok {
		return w.resumeSendOnReadiness(send)
	}
	state := w.active
	state.notReadyAliases = notReadyAliases(state.readinessRequirements, state.unavailable)
	if w.mustDiscard() {
		return w.discardUnavailableStage()
	}
	if len(state.notReadyAliases) != 0 {
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
		presentationMessage = fmt.Sprintf("staged message discarded: %q is no longer available", missing)
	}
	w.active = nil
	sequence := instructionSequence{
		appendRecordInstruction{record: room.Record{Kind: room.KindSystem, Text: message}},
	}
	switch state.statement.(type) {
	case promptlang.Send, promptlang.Broadcast, promptlang.Handoff:
		sequence = append(sequence, publishEventInstruction{event: StagedInputDiscarded{
			Raw: state.raw, Reason: presentationMessage,
		}})
	}
	return append(sequence, requestSnapshotInstruction{})
}

func (w *stageWorkflow) snapshot() *StagedSubmission {
	if w.active == nil || len(w.active.readinessRequirements) == 0 && len(w.active.routing) == 0 {
		return nil
	}
	return &StagedSubmission{
		Raw: w.active.raw, Routing: slices.Clone(w.active.routing),
		NotReadyAliases:    slices.Clone(w.active.notReadyAliases),
		Interruptible:      interruptibleStageAliases(w.active),
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

func freezeReadinessRequirements(state *stageState, participants []participantState) []participantState {
	switch state.statement.(type) {
	case promptlang.Send, promptlang.Broadcast:
	default:
		return slices.DeleteFunc(slices.Clone(participants), func(value participantState) bool {
			explicit := slices.Contains(handoffRouting(state.statement.(promptlang.Handoff)), value.alias)
			return !explicit && (!value.view().IsRoutable() || value.startupPending)
		})
	}
	byAlias := make(map[string]participantState, len(participants))
	for _, value := range participants {
		byAlias[value.alias] = value
	}
	readinessRequirements := make([]participantState, 0, len(state.routing))
	for _, alias := range state.routing {
		if value, ok := byAlias[alias]; ok {
			readinessRequirements = append(readinessRequirements, value)
		}
	}
	return readinessRequirements
}

func stageReadiness(readinessRequirements []participantState, routing []string) ([]string, []string) {
	byAlias := make(map[string]participant.Status, len(readinessRequirements))
	for _, value := range readinessRequirements {
		byAlias[value.alias] = value.status
	}
	var unavailable []string
	for _, alias := range routing {
		status, ok := byAlias[alias]
		if !ok || status == participant.StatusCrashed {
			unavailable = append(unavailable, alias)
		}
	}
	slices.Sort(unavailable)
	return notReadyAliases(readinessRequirements, unavailable), unavailable
}

func notReadyAliases(readinessRequirements []participantState, unavailable []string) []string {
	var blocked []string
	for _, value := range readinessRequirements {
		if !value.view().IsReadyForWork() && !slices.Contains(unavailable, value.alias) {
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

func interruptibleStageAliases(state *stageState) []string {
	var aliases []string
	for _, value := range state.readinessRequirements {
		if value.view().HasActiveTurn() && value.view().IsCancellable() &&
			slices.Contains(state.notReadyAliases, value.alias) && !slices.Contains(state.interrupted, value.alias) {
			aliases = append(aliases, value.alias)
		}
	}
	slices.Sort(aliases)
	return aliases
}

func (w *stageWorkflow) markStarted(alias string) {
	if slices.Contains(w.active.unavailable, alias) {
		return
	}
	for index := range w.active.readinessRequirements {
		if w.active.readinessRequirements[index].alias == alias {
			w.active.readinessRequirements[index].status = participant.StatusIdle
			w.active.readinessRequirements[index].startupPending = false
			return
		}
	}
}

func unavailableStageFailure(state *stageState) SubmissionFailed {
	failure := SubmissionFailed{Raw: state.raw, Operation: "staged dispatch", Code: ErrorExecutionFailed, Err: errNoStageTargets}
	send, ok := state.statement.(promptlang.Send)
	if !ok {
		return failure
	}
	failure.Code = ErrorParticipantUnavailable
	failure.Err = fmt.Errorf("%w %q", errStageTargetUnavailable, send.Alias)
	for _, value := range state.readinessRequirements {
		if value.alias == send.Alias && value.status == participant.StatusCrashed {
			failure.Err = fmt.Errorf("%w: %q", errStageTargetCrashed, send.Alias)
			return failure
		}
	}
	return failure
}
