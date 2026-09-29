package interpreter

import (
	"errors"
	"sync"

	"github.com/trigosec/coderoom/internal/agent"
	"github.com/trigosec/coderoom/internal/session"
)

var (
	errInvalidApprovalChoice    = errors.New("invalid approval choice")
	errApprovalNotActive        = errors.New("approval is not active")
	errApprovalChoiceNotOffered = errors.New("approval choice was not offered")
)

type resolveApprovalOperation struct {
	id     int64
	choice ApprovalChoice
}

// approvalSnapshotState is the temporary synchronized approval boundary used
// until snapshots are served from the immutable cache introduced in Step 5.
type approvalSnapshotState struct {
	mu       sync.RWMutex
	approval *Approval
}

// ResolveApproval queues a structured response to the active approval.
func (i *Interpreter) ResolveApproval(id int64, choice ApprovalChoice) error {
	return i.executor.resolveApproval(id, choice)
}

func (e *interpreterExecutor) resolveApproval(id int64, choice ApprovalChoice) error {
	if !e.enqueue(resolveApprovalOperation{id: id, choice: choice}) {
		return ErrClosed
	}
	return nil
}

func (op resolveApprovalOperation) apply(e *interpreterExecutor) {
	choice, err := e.approval.Choice(op.id, op.choice)
	if err != nil {
		e.publish(OperationFailed{Operation: "resolve approval", Err: err})
		return
	}
	err = e.session.Execute(session.ResolveApprovalCommand{ApprovalID: op.id, Choice: choice})
	if err != nil {
		e.publish(OperationFailed{Operation: "resolve approval", Err: err})
		return
	}
	e.approval.Clear(op.id)
	e.publish(StateChanged{Snapshot: e.captureSnapshot()})
}

func agentApprovalChoice(choice ApprovalChoice) (agent.ApprovalOption, bool) {
	option := agent.ApprovalOption(choice.OptionID)
	switch option {
	case agent.OptionAccept, agent.OptionAcceptForSession, agent.OptionDecline, agent.OptionCancel:
		return option, true
	default:
		return "", false
	}
}

func (s *approvalSnapshotState) Choice(id int64, choice ApprovalChoice) (agent.ApprovalOption, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.approval == nil || s.approval.ID != id {
		return "", errApprovalNotActive
	}
	for _, option := range s.approval.Options {
		if option.ID != choice.OptionID {
			continue
		}
		value, ok := agentApprovalChoice(choice)
		if !ok {
			return "", errInvalidApprovalChoice
		}
		return value, nil
	}
	return "", errApprovalChoiceNotOffered
}

func (s *approvalSnapshotState) Set(approval Approval) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.approval = &approval
}

func (s *approvalSnapshotState) Clear(id int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.approval == nil || s.approval.ID != id {
		return false
	}
	s.approval = nil
	return true
}

func (s *approvalSnapshotState) Snapshot() *Approval {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.approval == nil {
		return nil
	}
	approval := *s.approval
	approval.Options = append([]ApprovalOption(nil), approval.Options...)
	return &approval
}
