package monolith

import "github.com/roomscript/coderoom/internal/session"

// Incoming session facts are buffered for serialized handling. Publication to
// front ends is a separate boundary in api_events.go.
type sessionObserver struct{ executor *interpreterExecutor }

func (o sessionObserver) OnEvent(event session.Event) {
	o.executor.recordSessionEvent(event)
}

// ApplySessionEvents projects a complete event burst before queued command
// processing resumes, then requests the resulting snapshot.
func (r *instructionRunner) ApplySessionEvents(events []session.Event) instructionSequence {
	sequence := instructionSequence{}
	for _, event := range events {
		if !r.model.ApplySessionEvent(event) {
			continue
		}
		sequence = append(sequence, requestSnapshotInstruction{})
	}
	return sequence
}

// ApplySessionEvent projects observable state without running command policy.
// Unrelated facts still reach the room. Routing outcomes instead belong to the
// execution result; stale approval-clear events cannot clear a newer approval.
func (m *interpreterModel) ApplySessionEvent(event session.Event) bool {
	// Routing outcomes are consumed by the command completion, not a state projection.
	if _, ok := event.(session.RoutingCompleted); ok {
		return false
	}
	if !m.applyApprovalEvent(event) {
		return false
	}
	m.room.ApplyEvent(event)
	return true
}
