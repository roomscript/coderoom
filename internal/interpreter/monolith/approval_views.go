package monolith

import (
	"github.com/roomscript/coderoom/internal/agent"
	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/room"
)

func approvalFromAgent(id int64, alias participant.Alias, request agent.ApprovalRequest, presentation room.ApprovalPresentation) Approval {
	options := make([]ApprovalOption, len(request.Options))
	for index, option := range request.Options {
		options[index] = ApprovalOption{ID: string(option), Label: approvalOptionLabel(option)}
	}
	return Approval{
		ID:          id,
		Alias:       alias,
		Kind:        ApprovalKind(request.Kind),
		Summary:     presentation.Summary,
		Explanation: presentation.Explanation,
		Evidence:    presentation.Evidence,
		Paths:       presentation.Paths,
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
