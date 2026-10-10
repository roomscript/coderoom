package interpreter

import (
	"maps"
	"slices"

	"github.com/roomscript/coderoom/internal/interpreter/runtime"
)

type invocationCompletedOperation struct {
	id         uint64
	completion runtime.Completion
}

// goInvocation runs on the serialized path. The callback may run anywhere, but
// only enqueues; launch failure removes its identity before queued results apply.
func (e *interpreterExecutor) goInvocation(value goInvocationInstruction) instructionSequence {
	e.nextInvocationID++
	id := e.nextInvocationID
	if e.pendingInvocations == nil {
		e.pendingInvocations = make(map[uint64]submissionOutcome)
	}
	e.pendingInvocations[id] = value.outcome
	err := (runtime.CommandRunner{}).Go(value.command, value.context, func(completion runtime.Completion) {
		e.enqueueInvocationCompletion(invocationCompletedOperation{id: id, completion: completion})
	})
	if err != nil {
		delete(e.pendingInvocations, id)
		outcome := value.outcome
		outcome.err = err
		return submissionResultSequence(outcome)
	}
	return nil
}

func (op invocationCompletedOperation) apply(e *interpreterExecutor) {
	outcome, ok := e.pendingInvocations[op.id]
	if !ok {
		return
	}
	delete(e.pendingInvocations, op.id)
	sequence := make(instructionSequence, 0, len(op.completion.Records)+2)
	for _, record := range op.completion.Records {
		sequence = append(sequence, appendRecordInstruction{record: record})
	}
	outcome.err = op.completion.Err
	sequence.append(submissionResultSequence(outcome))
	e.runner.Run(sequence)
}

// Completion intake stays open while shutdown drains accepted operations. Results
// generated after submission admission closes are retained for the shutdown path.
func (e *interpreterExecutor) enqueueInvocationCompletion(op invocationCompletedOperation) {
	e.enqueueMu.Lock()
	defer e.enqueueMu.Unlock()
	if e.invocationCompletionsClosed {
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
func (e *interpreterExecutor) drainInvocationCompletions() {
	e.enqueueMu.Lock()
	e.invocationCompletionsClosed = true
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
		outcome.err = ErrClosed
		e.runner.Run(submissionResultSequence(outcome))
	}
}
