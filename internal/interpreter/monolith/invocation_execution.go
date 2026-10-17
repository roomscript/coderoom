package monolith

import (
	"fmt"
	"maps"
	"slices"

	"github.com/roomscript/coderoom/internal/interpreter/runtime"
	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/promptlang"
	"github.com/roomscript/coderoom/internal/room"
)

// participantViews supplies detached reads without live session access.
type participantViews []participant.View

func (views participantViews) Participants() []participant.View { return views }

type invocationCompletion struct {
	id         uint64
	completion runtime.CommandResult
	event      Event
}

// goInvocation runs on the serialized path. The callback may run anywhere, but
// only hands off results; launch failure removes its identity before results apply.
func (e *interpreterExecutor) goInvocation(value goInvocationInstruction) instructionSequence {
	e.nextInvocationID++
	id := e.nextInvocationID
	if e.pendingInvocations == nil {
		e.pendingInvocations = make(map[uint64]submissionOutcome)
	}
	e.pendingInvocations[id] = value.outcome
	if value.context.Participants == nil {
		value.context.Participants = participantViews(e.participants())
	}
	delivery := &commandSessionProvider{executor: e}
	if e.commandSessions == nil {
		e.commandSessions = make(map[uint64]*commandSessionProvider)
	}
	e.commandSessions[id] = delivery
	if value.context.Session == nil {
		value.context.Session = delivery
	}
	launcher := &userShellLauncher{executor: e}
	if value.context.Shell == nil {
		value.context.Shell = launcher
	}
	owned := &ownedCommand{Command: value.command}
	delivery.invocation = owned
	err := (runtime.CommandRunner{}).Go(owned, value.outcome.statement, value.context, func(completion runtime.CommandResult) {
		delivery.complete(invocationCompletion{id: id, completion: completion, event: launcher.completionEvent(value.outcome, completion.Records)})
	})
	if err != nil {
		delete(e.pendingInvocations, id)
		delivery.release()
		delete(e.commandSessions, id)
		outcome := value.outcome
		outcome.err = err
		return submissionResultSequence(outcome)
	}
	return withSubmissionSource(instructionSequence{publishEventInstruction{event: SubmissionSucceeded{Raw: value.outcome.raw}}}, value.outcome.statement)
}

func (e *interpreterExecutor) settleInvocation(op invocationCompletion) instructionSequence {
	outcome, ok := e.pendingInvocations[op.id]
	if !ok {
		return nil
	}
	delete(e.pendingInvocations, op.id)
	e.commandSessions[op.id].release()
	delete(e.commandSessions, op.id)
	return invocationCompletionSequence(outcome, op.completion, op.event)
}

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

// Shutdown fails only invocations left unfinished after retained results apply.
// Late callbacks cannot touch closed model state.
func (e *interpreterExecutor) failPendingInvocations() {
	for _, id := range slices.Sorted(maps.Keys(e.pendingInvocations)) {
		outcome := e.pendingInvocations[id]
		delete(e.pendingInvocations, id)
		e.commandSessions[id].release()
		delete(e.commandSessions, id)
		e.runner.Run(invocationCompletionSequence(outcome, runtime.CommandResult{Err: ErrClosed}, nil))
	}
}

// Retain cleanup without changing CommandRunner's preparation/launch ownership.
type ownedCommand struct {
	runtime.Command
	invocation runtime.Invocation
}

func (c *ownedCommand) Prepare(statement promptlang.ParsedStatement, ctx runtime.Context) (runtime.Invocation, error) {
	invocation, err := c.Command.Prepare(statement, ctx)
	c.invocation = invocation
	if err != nil {
		return nil, fmt.Errorf("prepare owned command: %w", err)
	}
	return invocation, nil
}
func (c *ownedCommand) close() {
	if c.invocation != nil {
		c.invocation.Close()
	}
}
