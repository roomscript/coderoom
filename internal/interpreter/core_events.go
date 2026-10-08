package interpreter

import "github.com/roomscript/coderoom/internal/session"

// Incoming session facts are buffered for serialized handling. Publication to
// front ends is a separate boundary in api_events.go.
type sessionObserver struct{ executor *interpreterExecutor }

func (o sessionObserver) OnEvent(event session.Event) {
	o.executor.recordSessionEvent(event)
}

// ApplySessionEvents updates the complete event burst before any resulting
// action executes. The runner settles those actions before delivery results.
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

// ApplySessionEvent first updates observable state, then advances affected work.
// Unrelated facts still reach the room. Routing outcomes instead belong to the
// execution result; stale approval-clear events cannot clear a newer approval.
func (m *interpreterModel) ApplySessionEvent(event session.Event) (instructionSequence, bool) {
	// Routing outcomes are consumed by the command completion, not a state projection.
	if _, ok := event.(session.RoutingCompleted); ok {
		return nil, false
	}
	if !m.applyApprovalEvent(event) {
		return nil, false
	}
	m.room.ApplyEvent(event)
	return m.workflows.applySessionEvent(event), true
}

// ApplyCompletion advances current work from an execution or shell result.
// Workflows reject results from superseded work; a synchronous delivery result
// reaches this entry point only after its causal session events settle.
func (m *interpreterModel) ApplyCompletion(completion workflowCompletion) instructionSequence {
	switch completion := completion.(type) {
	case submissionCompletion:
		return submissionResultSequence(completion)
	case rosterCompletion:
		return rosterResultSequence(completion)
	default:
		return m.workflows.applyCompletion(completion)
	}
}

// Shell operations are resumption points after asynchronous execution returns.
func (op shellCompletedOperation) apply(e *interpreterExecutor) {
	e.runner.Run(e.model.ApplyShellResult(op.command, e.cwd, op.result))
}

func (op workflowShellCompletedOperation) apply(e *interpreterExecutor) {
	e.runner.Run(e.model.ApplyCompletion(shellCompletion{
		target:  op.target,
		request: op.request,
		result:  op.result,
		cwd:     e.cwd,
	}))
}
