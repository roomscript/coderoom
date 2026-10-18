package monolith

import (
	"github.com/roomscript/coderoom/internal/interpreter/runtime"
	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/room"
)

// participantViews supplies detached reads without live session access.
type participantViews []participant.View

func (views participantViews) Participants() []participant.View { return views }

func invocationCompletionSequence(outcome submissionOutcome, completion runtime.CommandResult, event Event) instructionSequence {
	events := []Event{}
	if event != nil {
		events = append(events, event)
	} else if completion.Err != nil {
		events = append(events, OperationFailed{Raw: outcome.raw, Operation: outcome.operation, Err: completion.Err})
	}
	return withSubmissionSource(invocationPublicationSequence(completion.Records, events...), outcome.statement)
}

// invocationPublicationSequence describes output without settling any lifecycle.
func invocationPublicationSequence(records []room.Record, events ...Event) instructionSequence {
	sequence := make(instructionSequence, 0, len(records)+len(events)+1)
	for _, record := range records {
		sequence = append(sequence, appendRecordInstruction{record: record})
	}
	for _, event := range events {
		sequence = append(sequence, publishEventInstruction{event: event})
	}
	return append(sequence, publishSnapshotInstruction{})
}

// Completion intake stays open while shutdown drains accepted operations. Results
// generated after submission admission closes are retained for the shutdown path.
func (e *interpreterExecutor) enqueueCompletion(op operation) {
	e.enqueueMu.Lock()
	defer e.enqueueMu.Unlock()
	if e.completionsClosed {
		return
	}
	if e.closed {
		e.shutdownCompletions = append(e.shutdownCompletions, op)
		return
	}
	e.operations.Push(op)
}

// Called on the serialized path after accepted operations and owned workers
// finish. Close intake atomically before applying retained results.
func (e *interpreterExecutor) drainCompletions() {
	e.enqueueMu.Lock()
	e.completionsClosed = true
	completions := e.shutdownCompletions
	e.shutdownCompletions = nil
	e.enqueueMu.Unlock()
	for _, op := range completions {
		op.apply(e)
	}
}
