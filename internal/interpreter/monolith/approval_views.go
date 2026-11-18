package monolith

import (
	"github.com/roomscript/coderoom/internal/agent"
	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/room"
)

func approvalFromAgent(id int64, alias participant.Alias, request agent.ApprovalRequest) Approval {
	options := make([]ApprovalOption, len(request.Options))
	for index, option := range request.Options {
		options[index] = ApprovalOption{ID: string(option), Label: approvalOptionLabel(option)}
	}
	operation, _ := request.Details.Operation.(agent.CommandOperation)
	return Approval{
		ID:          id,
		Alias:       alias,
		Kind:        ApprovalKind(request.Kind),
		Prompt:      request.Ask,
		Summary:     room.CommandSummary(request),
		Explanation: operation.Explanation,
		Details:     room.ApprovalDetails(request),
		Paths:       room.ApprovalPaths(request),
		Options:     options,
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
