package interpreter

import (
	"errors"
	"fmt"

	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/promptlang"
	"github.com/roomscript/coderoom/internal/room"
	"github.com/roomscript/coderoom/internal/session"
)

type runnerItem interface{ runnerItem() }
type instructionItem struct{ instruction instruction }
type preparationItem struct{ result preparationResult }
type outcomeItem struct{ outcome executionOutcome }
type sessionEventsItem struct{ events []session.Event }

func (instructionItem) runnerItem()   {}
func (preparationItem) runnerItem()   {}
func (outcomeItem) runnerItem()       {}
func (sessionEventsItem) runnerItem() {}

func instructionItems(instructions instructionSequence) []runnerItem {
	items := make([]runnerItem, len(instructions))
	for index, instruction := range instructions {
		items[index] = instructionItem{instruction: instruction}
	}
	return items
}

type instructionExecutorPort interface {
	publish(Event)
	goInvocation(goInvocationInstruction) instructionSequence
	startWorkflowShell(startShellInstruction)
	startShell(string, string, string, promptlang.ParsedStatement)
	executeSessionRequest(sessionRequest) error
	executeCommand(session.Command) error
	createParticipantSendPlan(string) (session.ParticipantSendPlan, []string)
	planBroadcast() []string
	participantState() []participantState
	participants() []participant.View
	takeSessionEvents() []session.Event
	refreshSnapshot() Snapshot
	requestClose()
	shutdownSession()
}

type instructionModelPort interface {
	ApplyPreparation(preparationResult) instructionSequence
	ApplyOutcome(executionOutcome) instructionSequence
	ApplySessionEvent(session.Event) (instructionSequence, bool)
	AppendRecord(room.Record)
	TakeTranscriptChanges() []TranscriptChanged
	ReadHandoffSource(string) (session.HandoffSource, bool)
}

// instructionRunner owns causal instruction ordering. It knows how to execute
// the closed instruction vocabulary, but not which workflow produced it.
type instructionRunner struct {
	model    instructionModelPort
	executor instructionExecutorPort
}

func newInstructionRunner(model instructionModelPort, executor instructionExecutorPort) *instructionRunner {
	return &instructionRunner{model: model, executor: executor}
}

func (r *instructionRunner) Run(sequence instructionSequence) {
	queue := instructionItems(sequence)
	snapshotRequested := false
	for len(queue) > 0 {
		item := queue[0]
		queue = queue[1:]
		switch item := item.(type) {
		case instructionItem:
			follow, snapshot := r.apply(item.instruction)
			queue = append(follow, queue...)
			snapshotRequested = snapshotRequested || snapshot
		case preparationItem:
			follow := r.model.ApplyPreparation(item.result)
			queue = append(instructionItems(follow), queue...)
		case outcomeItem:
			follow := r.model.ApplyOutcome(item.outcome)
			queue = append(instructionItems(follow), queue...)
		case sessionEventsItem:
			// Project the complete dispatch burst before running instructions derived
			// from it, so those instructions observe all causal events.
			follow := r.ApplySessionEvents(item.events)
			queue = append(instructionItems(follow), queue...)
		default:
			panic(fmt.Sprintf("unknown instruction runner item %T", item))
		}
	}
	r.publishTranscriptChanges()
	if snapshotRequested {
		r.executor.publish(StateChanged{Snapshot: r.executor.refreshSnapshot()})
	}
}

func (r *instructionRunner) apply(value instruction) ([]runnerItem, bool) {
	if snapshot, handled := r.applyStateInstruction(value); handled {
		return nil, snapshot
	}
	if items, handled := r.applyPreparationInstruction(value); handled {
		return items, false
	}
	if items, snapshot, handled := r.applyExecutionInstruction(value); handled {
		return items, snapshot
	}
	if items, handled := r.applyLifecycleInstruction(value); handled {
		return items, false
	}
	panic(fmt.Sprintf("unknown workflow instruction %T", value))
}

func (r *instructionRunner) applyStateInstruction(value instruction) (bool, bool) {
	switch value := value.(type) {
	case appendRecordInstruction:
		r.model.AppendRecord(value.record)
		return false, true
	case publishEventInstruction:
		if _, accepted := value.event.(InputAccepted); !accepted {
			r.publishTranscriptChanges()
		}
		r.executor.publish(value.event)
		return false, true
	case publishSnapshotInstruction:
		r.publishTranscriptChanges()
		r.executor.publish(StateChanged{Snapshot: r.executor.refreshSnapshot()})
		return false, true
	case requestSnapshotInstruction:
		return true, true
	default:
		return false, false
	}
}

func (r *instructionRunner) applyExecutionInstruction(value instruction) ([]runnerItem, bool, bool) {
	switch value := value.(type) {
	case goInvocationInstruction:
		return instructionItems(r.executor.goInvocation(value)), false, true
	case executeCommandInstruction:
		items, snapshot := r.executeCommand(value)
		return items, snapshot, true
	case executeSessionInstruction:
		return r.executeSession(value), false, true
	default:
		return nil, false, false
	}
}

// Preparation reads return facts immediately; they never introduce a wait.
func (r *instructionRunner) applyPreparationInstruction(value instruction) ([]runnerItem, bool) {
	switch value := value.(type) {
	case readParticipantsInstruction:
		return []runnerItem{preparationItem{result: participantsResult{
			raw: value.raw, statement: value.statement, participants: r.executor.participants(),
		}}}, true
	case prepareSendInstruction:
		plan, targets := r.executor.createParticipantSendPlan(value.alias)
		return []runnerItem{preparationItem{result: sendPlanResult{
			target: value.target, plan: plan, targets: targets, participants: r.executor.participantState(),
		}}}, true
	case planBroadcastInstruction:
		targets := r.executor.planBroadcast()
		return []runnerItem{preparationItem{result: broadcastPlanResult{
			target: value.target, targets: targets,
		}}}, true
	case readParticipantStateInstruction:
		readinessRequirements := r.executor.participantState()
		return []runnerItem{preparationItem{result: participantStateResult{
			target: value.target, readinessRequirements: readinessRequirements,
		}}}, true
	case readHandoffSourceInstruction:
		source, ok := r.model.ReadHandoffSource(value.alias)
		return []runnerItem{preparationItem{result: handoffSourceResult{
			target: value.target, source: source, ok: ok,
		}}}, true
	default:
		return nil, false
	}
}

func (r *instructionRunner) applyLifecycleInstruction(value instruction) ([]runnerItem, bool) {
	switch value := value.(type) {
	case startShellInstruction:
		r.executor.startWorkflowShell(value)
		return nil, true
	case startUserShellInstruction:
		r.executor.startShell(value.raw, value.command, value.program, value.statement)
		return nil, true
	case requestCloseInstruction:
		r.executor.requestClose()
		return nil, true
	case shutdownSessionInstruction:
		r.executor.shutdownSession()
		sequence := materializeSnapshot(r.ApplySessionEvents(r.executor.takeSessionEvents()))
		return instructionItems(sequence), true
	default:
		return nil, false
	}
}

func (r *instructionRunner) executeCommand(value executeCommandInstruction) ([]runnerItem, bool) {
	outcome := value.outcome
	outcome.err = r.executor.executeCommand(value.command)
	eventSequence := materializeSnapshot(r.ApplySessionEvents(r.executor.takeSessionEvents()))
	items := instructionItems(eventSequence)
	items = append(items, outcomeItem{outcome: outcome})
	return items, false
}

func materializeSnapshot(sequence instructionSequence) instructionSequence {
	result := make(instructionSequence, 0, len(sequence))
	requested := false
	for _, item := range sequence {
		if _, ok := item.(requestSnapshotInstruction); ok {
			requested = true
			continue
		}
		result = append(result, item)
	}
	if requested {
		result = append(result, publishSnapshotInstruction{})
	}
	return result
}

func (r *instructionRunner) executeSession(value executeSessionInstruction) []runnerItem {
	err := r.executor.executeSessionRequest(value.request)
	events := r.executor.takeSessionEvents()
	routing := routingResultFromEvents(events)
	accepted, err := routingAcceptance(value.request, routing, err)
	applySuccessRecords := accepted && len(value.recordsOnSuccess) != 0
	items := []runnerItem{}
	if applySuccessRecords {
		for _, record := range value.recordsOnSuccess {
			if routing.Kind != "" {
				record = recordWithRoutingResult(record, routing)
			}
			items = append(items, instructionItem{instruction: appendRecordInstruction{record: record}})
		}
	}
	items = append(items, sessionEventsItem{events: events})
	items = append(items, outcomeItem{outcome: sessionOutcome{
		target:  value.target,
		routing: routing,
		err:     err,
	}})
	return items
}

func (r *instructionRunner) publishTranscriptChanges() {
	for _, change := range r.model.TakeTranscriptChanges() {
		r.executor.publish(change)
	}
}

// Routing commands are serialized, and emit exactly one outcome before returning.
// The burst may also contain unrelated lifecycle messages, but no other routing command.
func routingResultFromEvents(events []session.Event) session.RoutingResult {
	for _, event := range events {
		if completed, ok := event.(session.RoutingCompleted); ok {
			return completed.Result.Clone()
		}
	}
	return session.RoutingResult{}
}

func recordWithRoutingResult(record room.Record, result session.RoutingResult) room.Record {
	record.Routing = result.Aliases(session.DeliveryDelivered)
	record.FailedRouting = result.Aliases(session.DeliveryFailed)
	record.UnsentRouting = result.Aliases(session.DeliveryNotAttempted)
	return record
}

var errMissingRoutingOutcome = errors.New("routing command returned without a routing outcome")

func routingAcceptance(request sessionRequest, result session.RoutingResult, err error) (bool, error) {
	switch request.(type) {
	case createPlanAndExecuteParticipantSendRequest, executePlannedParticipantSendRequest, broadcastRequest, handoffRequest:
		if result.Kind == "" {
			return false, errors.Join(err, errMissingRoutingOutcome)
		}
		return len(result.Aliases(session.DeliveryDelivered)) != 0, err
	default:
		return err == nil, err
	}
}
