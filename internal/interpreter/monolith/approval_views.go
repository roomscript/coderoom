package monolith

import (
	"github.com/roomscript/coderoom/internal/agent"
	"github.com/roomscript/coderoom/internal/participant"
)

func approvalFromAgent(id int64, alias participant.Alias, request agent.ApprovalRequest) Approval {
	options := make([]ApprovalOption, len(request.Options))
	for index, option := range request.Options {
		options[index] = ApprovalOption{ID: string(option), Label: approvalOptionLabel(option)}
	}
	return Approval{
		ID:      id,
		Alias:   alias,
		Kind:    ApprovalKind(request.Kind),
		Prompt:  request.Ask,
		Options: options,
	}
}

func approvalOptionLabel(option agent.ApprovalOption) string {
	switch option {
	case agent.OptionAccept:
		return "Accept"
	case agent.OptionAcceptForSession:
		return "Accept for session"
	case agent.OptionDecline:
		return "Decline"
	case agent.OptionCancel:
		return "Cancel"
	default:
		return string(option)
	}
}
