package monolith

import (
	"fmt"
	"strings"

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
		Details: approvalDisplayDetails(request),
		Options: options,
	}
}

func approvalDisplayDetails(request agent.ApprovalRequest) string {
	switch operation := request.Details.Operation.(type) {
	case agent.CommandOperation:
		text := "Command: " + operation.Command
		if operation.Cwd != "" {
			text += "\nWorking directory: " + operation.Cwd
		}
		return text
	case agent.FileOperation:
		return fileApprovalDisplayDetails(operation)
	}
	if request.Details.Display != "" {
		return request.Details.Display
	}
	if request.Kind == agent.ApprovalFileChange {
		return "Proposed file details unavailable: no matching file-change evidence."
	}
	return ""
}

func fileApprovalDisplayDetails(operation agent.FileOperation) string {
	if len(operation.ProposedChanges) == 0 {
		text := strings.Join(operation.Paths, "\n")
		if operation.Write {
			if text != "" {
				text += "\n"
			}
			text += "Proposed file details unavailable: no matching file-change evidence."
		}
		return text
	}
	var text strings.Builder
	for i, change := range operation.ProposedChanges {
		if i > 0 {
			text.WriteString("\n\n")
		}
		fmt.Fprintf(&text, "%s: %s\n%s", change.ChangeKind, change.Path, change.Diff)
	}
	return text.String()
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
