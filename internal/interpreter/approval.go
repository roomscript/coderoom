package interpreter

import (
	"errors"

	"github.com/roomscript/coderoom/internal/agent"
	"github.com/roomscript/coderoom/internal/session"
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
	choice, err := e.model.ResolveApprovalChoice(op.id, op.choice)
	if err != nil {
		e.publish(OperationFailed{Operation: "resolve approval", Err: err})
		return
	}
	err = e.session.Execute(session.ResolveApprovalCommand{ApprovalID: op.id, Choice: choice})
	if err != nil {
		e.publish(OperationFailed{Operation: "resolve approval", Err: err})
		return
	}
	e.model.ClearApproval(op.id)
	e.publish(StateChanged{Snapshot: e.refreshSnapshot()})
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

func (m *interpreterModel) ResolveApprovalChoice(
	id int64,
	choice ApprovalChoice,
) (agent.ApprovalOption, error) {
	if m.approval == nil || m.approval.ID != id {
		return "", errApprovalNotActive
	}
	for _, option := range m.approval.Options {
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

func (m *interpreterModel) applyApprovalEvent(event session.Event) bool {
	switch event := event.(type) {
	case session.ApprovalRequested:
		approval := approvalFromAgent(event.ID, event.Alias, event.Req)
		m.approval = &approval
	case session.ApprovalCleared:
		return m.ClearApproval(event.ID)
	}
	return true
}

func (m *interpreterModel) ClearApproval(id int64) bool {
	if m.approval == nil || m.approval.ID != id {
		return false
	}
	m.approval = nil
	return true
}

func cloneApproval(source *Approval) *Approval {
	if source == nil {
		return nil
	}
	approval := *source
	approval.Options = append([]ApprovalOption(nil), approval.Options...)
	return &approval
}
