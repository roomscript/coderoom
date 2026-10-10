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
	loopSource, stageSource := m.workflows.loop.statement(), m.workflows.stage.statement()
	actions := withSubmissionSource(m.workflows.loop.handleSessionEvent(event), loopSource)
	actions.append(withSubmissionSource(m.workflows.stage.handleSessionEvent(event), stageSource))
	return actions, true
}

// ApplyPreparation applies synchronous planning and inspection facts. No work
// is suspended while these reads run; workflows may retain the prepared plan.
func (m *interpreterModel) ApplyPreparation(result preparationResult) instructionSequence {
	source := m.workflows.stage.statement()
	return withSubmissionSource(m.applyPreparation(result), source)
}

func (m *interpreterModel) applyPreparation(result preparationResult) instructionSequence {
	switch result := result.(type) {
	case sendPlanResult:
		return m.workflows.stage.prepareSend(result)
	case broadcastPlanResult:
		return m.workflows.stage.handleBroadcastPlan(result)
	case participantStateResult:
		return m.workflows.stage.handleParticipantState(result)
	case handoffSourceResult:
		return m.workflows.stage.handleHandoffSource(result)
	}
	return nil
}

// ApplyOutcome handles execution outcomes. Session commands are synchronous and
// their causal events settle first. A later shell outcome resumes suspended work.
func (m *interpreterModel) ApplyOutcome(outcome executionOutcome) instructionSequence {
	switch outcome := outcome.(type) {
	case sessionOutcome:
		return m.applySessionOutcome(outcome)
	case shellOutcome:
		if outcome.target.kind == workflowLoop {
			return withSubmissionSource(m.workflows.loop.resumeOnConditionResult(outcome), outcome.request.statement)
		}
	case submissionOutcome:
		return submissionResultSequence(outcome)
	}
	return nil
}

func (m *interpreterModel) applySessionOutcome(result sessionOutcome) instructionSequence {
	switch result.target.kind {
	case workflowLoop:
		source := m.workflows.loop.statement()
		return withSubmissionSource(m.workflows.loop.finishParticipantDelivery(result), source)
	case workflowStage:
		source := m.workflows.stage.statement()
		return withSubmissionSource(m.workflows.stage.applySessionOutcome(result), source)
	default:
		return nil
	}
}

// Shell operations are resumption points after asynchronous execution returns.
func (op shellCompletedOperation) apply(e *interpreterExecutor) {
	e.runner.Run(e.model.ApplyShellResult(op.raw, op.command, e.cwd, op.result, op.completion, op.statement))
}

func (op workflowShellCompletedOperation) apply(e *interpreterExecutor) {
	e.runner.Run(e.model.ApplyOutcome(shellOutcome{
		target:  op.target,
		request: op.request,
		result:  op.result,
		cwd:     e.cwd,
	}))
}
