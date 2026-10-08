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
	actions := m.workflows.loop.handleSessionEvent(event)
	actions.append(m.workflows.stage.handleSessionEvent(event))
	return actions, true
}

// ApplyResult distinguishes synchronous preparation facts from execution outcomes.
// A later shell result resumes a loop; session outcomes arrive after their causal
// events settle. Each workflow checks that the result still belongs to its work.
func (m *interpreterModel) ApplyResult(result workflowCompletion) instructionSequence {
	switch result := result.(type) {
	case sendPlanResult:
		return m.workflows.stage.prepareSend(result)
	case broadcastPlanResult:
		return m.workflows.stage.handleBroadcastPlan(result)
	case participantStateResult:
		return m.workflows.stage.handleParticipantState(result)
	case handoffSourceResult:
		return m.workflows.stage.handleHandoffSource(result)
	case sessionCompletion:
		return m.applySessionOutcome(result)
	case shellCompletion:
		if result.target.kind == workflowLoop {
			return m.workflows.loop.handleShellCompletion(result)
		}
	case submissionCompletion:
		return submissionResultSequence(result)
	case rosterCompletion:
		return rosterResultSequence(result)
	}
	return nil
}

func (m *interpreterModel) applySessionOutcome(result sessionCompletion) instructionSequence {
	switch result.target.kind {
	case workflowLoop:
		return m.workflows.loop.handleSessionCompletion(result)
	case workflowStage:
		return m.workflows.stage.applySessionOutcome(result)
	default:
		return nil
	}
}

// Shell operations are resumption points after asynchronous execution returns.
func (op shellCompletedOperation) apply(e *interpreterExecutor) {
	e.runner.Run(e.model.ApplyShellResult(op.command, e.cwd, op.result))
}

func (op workflowShellCompletedOperation) apply(e *interpreterExecutor) {
	e.runner.Run(e.model.ApplyResult(shellCompletion{
		target:  op.target,
		request: op.request,
		result:  op.result,
		cwd:     e.cwd,
	}))
}
