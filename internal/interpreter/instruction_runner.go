package interpreter

import (
	"fmt"

	"github.com/trigosec/coderoom/internal/participant"
	"github.com/trigosec/coderoom/internal/room"
	"github.com/trigosec/coderoom/internal/session"
)

type instructionExecutorPort interface {
	publish(Event)
	startWorkflowShell(startShellInstruction)
	startShell(string, string, string)
	executeSessionRequest(sessionRequest) error
	executeCommand(session.Command) error
	planSharedSend(string) (session.SharedSendPlan, []string)
	planBroadcast() []string
	participantState() []participantState
	roster() []participant.View
	takeSessionEvents() []session.Event
	refreshSnapshot() Snapshot
	requestClose()
	shutdownSession()
}

type instructionModelPort interface {
	ApplyCompletion(workflowCompletion) instructionSequence
	ApplySessionEvent(session.Event) (instructionSequence, bool)
	AppendRecord(room.Record)
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
		case completionItem:
			follow := r.model.ApplyCompletion(item.completion)
			queue = append(instructionItems(follow), queue...)
		default:
			panic(fmt.Sprintf("unknown instruction runner item %T", item))
		}
	}
	if snapshotRequested {
		r.executor.publish(StateChanged{Snapshot: r.executor.refreshSnapshot()})
	}
}

func (r *instructionRunner) apply(value instruction) ([]executorItem, bool) {
	if snapshot, handled := r.applyStateInstruction(value); handled {
		return nil, snapshot
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
		r.executor.publish(value.event)
		return false, true
	case publishSnapshotInstruction:
		r.executor.publish(StateChanged{Snapshot: r.executor.refreshSnapshot()})
		return false, true
	case requestSnapshotInstruction:
		return true, true
	default:
		return false, false
	}
}

func (r *instructionRunner) applyExecutionInstruction(value instruction) ([]executorItem, bool, bool) {
	switch value := value.(type) {
	case executeCommandInstruction:
		items, snapshot := r.executeCommand(value)
		return items, snapshot, true
	case executeSessionInstruction:
		items, snapshot := r.executeSession(value)
		return items, snapshot, true
	case readRosterInstruction:
		return []executorItem{completionItem{completion: rosterCompletion{
			raw: value.raw, participants: r.executor.roster(),
		}}}, false, true
	case planSharedSendInstruction:
		plan, targets := r.executor.planSharedSend(value.alias)
		return []executorItem{completionItem{completion: sharedSendPlanResult{
			target: value.target, plan: plan, targets: targets,
		}}}, false, true
	case planBroadcastInstruction:
		targets := r.executor.planBroadcast()
		return []executorItem{completionItem{completion: broadcastPlanResult{
			target: value.target, targets: targets,
		}}}, false, true
	case readParticipantStateInstruction:
		barrier := r.executor.participantState()
		return []executorItem{completionItem{completion: participantStateResult{
			target: value.target, barrier: barrier,
		}}}, false, true
	default:
		return nil, false, false
	}
}

func (r *instructionRunner) applyLifecycleInstruction(value instruction) ([]executorItem, bool) {
	switch value := value.(type) {
	case startShellInstruction:
		r.executor.startWorkflowShell(value)
		return nil, true
	case startUserShellInstruction:
		r.executor.startShell(value.raw, value.command, value.program)
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

func (r *instructionRunner) executeCommand(value executeCommandInstruction) ([]executorItem, bool) {
	completion := value.completion
	completion.err = r.executor.executeCommand(value.command)
	eventSequence := materializeSnapshot(r.ApplySessionEvents(r.executor.takeSessionEvents()))
	items := instructionItems(eventSequence)
	items = append(items, completionItem{completion: completion})
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

func (r *instructionRunner) executeSession(value executeSessionInstruction) ([]executorItem, bool) {
	err := r.executor.executeSessionRequest(value.request)
	events := r.executor.takeSessionEvents()
	eventSequence := r.ApplySessionEvents(events)
	items := instructionItems(eventSequence)
	items = append(items, completionItem{completion: sessionCompletion{target: value.target, err: err}})
	return items, false
}

func (r *instructionRunner) ApplySessionEvents(events []session.Event) instructionSequence {
	sequence := instructionSequence{}
	for _, event := range events {
		eventSequence, applied := r.model.ApplySessionEvent(event)
		if !applied {
			continue
		}
		sequence.append(eventSequence)
		sequence = append(sequence, requestSnapshotInstruction{})
	}
	return sequence
}
