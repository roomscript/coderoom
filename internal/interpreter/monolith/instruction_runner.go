package monolith

import (
	"fmt"

	"github.com/roomscript/coderoom/internal/room"
	"github.com/roomscript/coderoom/internal/session"
)

type runnerItem interface{ runnerItem() }
type instructionItem struct{ instruction instruction }
type sessionEventsItem struct{ events []session.Event }

func (instructionItem) runnerItem()   {}
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
	refreshSnapshot() Snapshot
}

type instructionModelPort interface {
	ApplySessionEvent(session.Event) bool
	AppendRecord(room.Record)
	TakeTranscriptChanges() []TranscriptChanged
}

// instructionRunner orders canonical records, events and snapshots. It contains
// no command dispatch, workflow policy or invocation lifecycle ownership.
type instructionRunner struct {
	model    instructionModelPort
	executor instructionExecutorPort
}

func newInstructionRunner(model instructionModelPort, executor instructionExecutorPort) *instructionRunner {
	return &instructionRunner{model: model, executor: executor}
}

func (r *instructionRunner) Run(sequence instructionSequence) {
	r.runItems(instructionItems(sequence))
}

func (r *instructionRunner) runItems(queue []runnerItem) {
	snapshotRequested := false
	for len(queue) > 0 {
		item := queue[0]
		queue = queue[1:]
		switch item := item.(type) {
		case instructionItem:
			snapshotRequested = r.apply(item.instruction) || snapshotRequested
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

func (r *instructionRunner) apply(value instruction) bool {
	switch value := value.(type) {
	case appendRecordInstruction:
		r.model.AppendRecord(value.record)
	case publishEventInstruction:
		if _, accepted := value.event.(InputAccepted); !accepted {
			r.publishTranscriptChanges()
		}
		r.executor.publish(value.event)
	case publishSnapshotInstruction:
		r.publishTranscriptChanges()
		r.executor.publish(StateChanged{Snapshot: r.executor.refreshSnapshot()})
	case requestSnapshotInstruction:
		return true
	default:
		panic(fmt.Sprintf("unknown publication instruction %T", value))
	}
	return false
}

func (r *instructionRunner) publishTranscriptChanges() {
	for _, change := range r.model.TakeTranscriptChanges() {
		r.executor.publish(change)
	}
}
