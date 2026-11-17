package room

import (
	"fmt"
	"slices"
	"strings"

	"github.com/roomscript/coderoom/internal/agent"
	"github.com/roomscript/coderoom/internal/session"
)

func (r *Room) handleApprovalEventLocked(e session.Event) ([]int, bool) {
	switch e := e.(type) {
	case session.ApprovalRequested:
		if e.Req.Kind != agent.ApprovalFileChange {
			return nil, false
		}
		text := "⚡ Approval: File changes\n" + e.Req.Ask + "\nFiles:\n" + strings.Join(ApprovalPaths(e.Req), "\n") + "\n\n" + ApprovalDetails(e.Req)
		return r.appendRecordLocked(Record{Kind: KindApproval, Alias: e.Alias, ApprovalID: e.ID, Text: text, Decision: "Waiting for you"}), true
	case session.ApprovalCleared:
		for i := len(r.records) - 1; i >= 0; i-- {
			record := &r.records[i]
			if record.Kind == KindApproval && record.ApprovalID == e.ID && record.Alias == e.Alias {
				record.Decision = approvalDecision(e.Choice)
				return []int{i}, true
			}
		}
	}
	return nil, false
}

// ApprovalPaths returns detached affected paths from normalized file operations.
func ApprovalPaths(request agent.ApprovalRequest) []string {
	operation, ok := request.Details.Operation.(agent.FileOperation)
	if !ok {
		return nil
	}
	paths := slices.Clone(operation.Paths)
	for _, change := range operation.ProposedChanges {
		if !slices.Contains(paths, change.Path) {
			paths = append(paths, change.Path)
		}
	}
	return paths
}

// ApprovalDetails returns complete display evidence, without backend metadata.
func ApprovalDetails(request agent.ApprovalRequest) string {
	switch operation := request.Details.Operation.(type) {
	case agent.CommandOperation:
		text := "Command: " + operation.Command
		if operation.Cwd != "" {
			text += "\nWorking directory: " + operation.Cwd
		}
		return text
	case agent.FileOperation:
		if request.Kind == agent.ApprovalFileChange {
			operation.Write = true
		}
		return fileApprovalDetails(operation)
	}
	if request.Details.Display != "" {
		return request.Details.Display
	}
	if request.Kind == agent.ApprovalFileChange {
		return "Proposed file details unavailable: no matching file-change evidence."
	}
	return ""
}

func fileApprovalDetails(operation agent.FileOperation) string {
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
	covered := make(map[string]bool, len(operation.ProposedChanges))
	for i, change := range operation.ProposedChanges {
		covered[change.Path] = true
		if i > 0 {
			text.WriteString("\n\n")
		}
		fmt.Fprintf(&text, "%s: %s\n%s", change.ChangeKind, change.Path, change.Diff)
		if change.Diff == "" {
			text.WriteString("Proposed file details unavailable: no matching file-change evidence.")
		}
	}
	for _, path := range operation.Paths {
		if !covered[path] {
			fmt.Fprintf(&text, "\n\n%s\nProposed file details unavailable: no matching file-change evidence.", path)
		}
	}
	return text.String()
}

func approvalDecision(choice agent.ApprovalOption) string {
	switch choice {
	case agent.OptionAccept:
		return "Approved by you · once"
	case agent.OptionAcceptForSession:
		return "Approved by you · for session"
	case agent.OptionDecline:
		return "Declined by you"
	case agent.OptionCancel:
		return "Canceled by you"
	default:
		return "Withdrawn"
	}
}
