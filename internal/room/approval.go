package room

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/roomscript/coderoom/internal/agent"
	"github.com/roomscript/coderoom/internal/approval"
	"github.com/roomscript/coderoom/internal/session"
)

// CommandSummary derives a label without changing the request's operation.
func CommandSummary(request agent.ApprovalRequest) string {
	operation, _ := request.Details.Operation.(agent.CommandOperation)
	return approval.CommandSummary(operation.Command)
}

// WithWorkspaceRoot configures the authoritative project scope for display paths.
func WithWorkspaceRoot(root string) Option {
	return func(r *Room) { r.workspaceRoot = root }
}

// ApprovalPaths returns display paths without changing the request's targets.
func (r *Room) ApprovalPaths(request agent.ApprovalRequest) []string {
	paths := ApprovalPaths(request)
	for i, path := range paths {
		paths[i] = r.approvalPath(path)
	}
	return paths
}

func (r *Room) approvalPath(path string) string {
	if !filepath.IsAbs(r.workspaceRoot) || !filepath.IsAbs(path) {
		return path
	}
	rel, err := filepath.Rel(r.workspaceRoot, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return path
	}
	return rel
}

func (r *Room) handleApprovalEventLocked(e session.Event) ([]int, bool) {
	switch e := e.(type) {
	case session.ApprovalRequested:
		text := r.approvalText(e.Req)
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

func (r *Room) approvalText(req agent.ApprovalRequest) string {
	presentation := r.DeriveApprovalPresentation(req)
	text := "⚡ Approval: " + presentation.Summary
	if req.Kind == agent.ApprovalCommandExecution {
		operation, _ := req.Details.Operation.(agent.CommandOperation)
		cwd := operation.Cwd
		if cwd == "" {
			cwd = "unknown"
		}
		text += " (cwd: " + cwd + ")"
	}
	if presentation.Explanation != "" {
		text += "\nExplanation: " + presentation.Explanation
	}
	return text + "\n" + presentation.Evidence
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

// ApprovalPresentation is a detached derivation shared by history and the panel.
// Evidence excludes the explanation and heading; it is not a complete document.
type ApprovalPresentation struct {
	Summary     string
	Explanation string
	Evidence    string
	Paths       []string
}

// DeriveApprovalPresentation derives display data without workspace path shortening.
func DeriveApprovalPresentation(request agent.ApprovalRequest) ApprovalPresentation {
	return (&Room{}).DeriveApprovalPresentation(request)
}

// DeriveApprovalPresentation composes operation and adapter evidence without mutation.
func (r *Room) DeriveApprovalPresentation(request agent.ApprovalRequest) ApprovalPresentation {
	p := ApprovalPresentation{Summary: deriveApprovalSummary(request), Explanation: request.Presentation.Explanation}
	switch request.Kind {
	case agent.ApprovalCommandExecution:
		operation, _ := request.Details.Operation.(agent.CommandOperation)
		p.Evidence = commandApprovalEvidence(operation)
	case agent.ApprovalFileChange:
		p.Paths = r.ApprovalPaths(request)
		operation, _ := request.Details.Operation.(agent.FileOperation)
		operation.Write = true
		p.Evidence = "Files:\n" + strings.Join(p.Paths, "\n") + "\n\n" + r.fileApprovalDetails(operation)
	case agent.ApprovalPermissions:
		// Permission operations inform policy; adapters retain the full requested action.
	}
	if extra := request.Presentation.Evidence; extra != "" {
		if p.Evidence != "" {
			p.Evidence += "\n\n"
		}
		p.Evidence += extra
	}
	if p.Evidence == "" {
		p.Evidence = "Request evidence unavailable."
	}
	return p
}

func deriveApprovalSummary(request agent.ApprovalRequest) string {
	switch request.Kind {
	case agent.ApprovalCommandExecution:
		return CommandSummary(request)
	case agent.ApprovalFileChange:
		return "File changes"
	default:
		if request.Presentation.Summary != "" {
			return request.Presentation.Summary
		}
		if request.Kind == agent.ApprovalPermissions {
			return "Permission request"
		}
		return "Approval request"
	}
}

func commandApprovalEvidence(operation agent.CommandOperation) string {
	text := "Command: " + operation.Command
	if strings.TrimSpace(operation.Command) == "" {
		text = "Command evidence unavailable."
	}
	return text
}

func (r *Room) fileApprovalDetails(operation agent.FileOperation) string {
	if len(operation.ProposedChanges) == 0 {
		paths := slices.Clone(operation.Paths)
		for i, path := range paths {
			paths[i] = r.approvalPath(path)
		}
		text := strings.Join(paths, "\n")
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
		fmt.Fprintf(&text, "%s: %s\n%s", change.ChangeKind, r.approvalPath(change.Path), change.Diff)
		if change.Diff == "" {
			text.WriteString("Proposed file details unavailable: no matching file-change evidence.")
		}
	}
	for _, path := range operation.Paths {
		if !covered[path] {
			fmt.Fprintf(&text, "\n\n%s\nProposed file details unavailable: no matching file-change evidence.", r.approvalPath(path))
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
