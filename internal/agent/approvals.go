package agent

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
)

// ApprovalListener receives normalized approval requests from an agent backend
// and returns a normalized decision. ctx is cancelled when the agent shuts down;
// implementations should respect it to avoid blocking indefinitely.
type ApprovalListener interface {
	Decide(ctx context.Context, req ApprovalRequest) (ApprovalDecision, error)
}

// ApprovalKind identifies the type of approval requested.
type ApprovalKind string

const (
	// ApprovalCommandExecution indicates an approval request for running a command.
	ApprovalCommandExecution ApprovalKind = "commandExecution"
	// ApprovalFileChange indicates an approval request for writing files / applying file changes.
	ApprovalFileChange ApprovalKind = "fileChange"
	// ApprovalPermissions indicates an approval request for granting additional permissions.
	ApprovalPermissions ApprovalKind = "permissions"
)

// ApprovalRequest is a normalized, user-facing approval prompt.
type ApprovalRequest struct {
	Kind         ApprovalKind
	Details      ApprovalDetails
	Presentation ApprovalPresentation

	// Options is the set of valid choices for this request.
	Options []ApprovalOption
}

// ApprovalDetails retains backend input and one operation needing approval.
type ApprovalDetails struct {
	Backend   string
	RequestID string
	Tool      string
	Raw       json.RawMessage
	Operation Operation
}

// ApprovalPresentation contains unshortened display inputs, never policy inputs.
// Room derives command/file summaries and operation evidence. Adapters supply
// other labels and supplemental evidence, excluding extracted explanations.
type ApprovalPresentation struct {
	Summary     string
	Explanation string
	Evidence    string
}

// ApprovalPrompt derives diagnostic context without storing a second presentation.
func ApprovalPrompt(request ApprovalRequest) string {
	text := approvalDiagnosticSummary(request)
	if request.Presentation.Explanation != "" {
		text += " — " + request.Presentation.Explanation
	}
	if request.Kind == ApprovalCommandExecution || request.Kind == ApprovalFileChange {
		if context := approvalOperationContext(request.Details.Operation); context != "" {
			text += "\n" + context
		}
		if request.Presentation.Evidence != "" {
			text += "\n" + request.Presentation.Evidence
		}
	}
	return text
}

func approvalDiagnosticSummary(request ApprovalRequest) string {
	if request.Presentation.Summary != "" {
		return request.Presentation.Summary
	}
	switch request.Kind {
	case ApprovalCommandExecution:
		return "shell command"
	case ApprovalFileChange:
		return "File changes"
	case ApprovalPermissions:
		return "Permission request"
	default:
		return "Approval request"
	}
}

func approvalOperationContext(operation Operation) string {
	switch operation := operation.(type) {
	case CommandOperation:
		cwd := operation.Cwd
		if cwd == "" {
			cwd = "unknown"
		}
		return "Command: " + operation.Command + "\nWorking directory: " + cwd
	case FileOperation:
		paths := slices.Clone(operation.Paths)
		for _, change := range operation.ProposedChanges {
			if !slices.Contains(paths, change.Path) {
				paths = append(paths, change.Path)
			}
		}
		if len(paths) > 0 {
			return "Files:\n" + strings.Join(paths, "\n")
		}
	}
	return ""
}

// Operation is a sealed approval payload. Evaluators support the concrete value
// types below; absent or unsupported payloads require human review.
type Operation interface{ approvalOperation() }

// CommandOperation describes a shell command and its authoritative working directory.
type CommandOperation struct {
	Command string
	Cwd     string
}

// FileOperation describes file access and whether every target is known.
type FileOperation struct {
	Cwd             string
	Paths           []string
	Write           bool
	AllTargetsKnown bool
	// ProposedChanges is display evidence; it does not validate Paths or AllTargetsKnown.
	ProposedChanges []FileChange
}

// HTTPOperation contains the URL and explicit outbound inputs reported by a backend.
// Screening covers these inputs, not implicit transport headers or backend traffic.
type HTTPOperation struct {
	Method           string
	URL              string
	Headers          map[string]string
	Body             string
	FollowsRedirects bool
	Dynamic          bool
}

func (CommandOperation) approvalOperation() {}
func (FileOperation) approvalOperation()    {}
func (HTTPOperation) approvalOperation()    {}

// ApprovalOption is a normalized approval response choice.
type ApprovalOption string

const (
	// OptionAccept approves the requested action.
	OptionAccept ApprovalOption = "accept"
	// OptionAcceptForSession approves the requested action and caches it for the session when supported.
	OptionAcceptForSession ApprovalOption = "acceptForSession"
	// OptionDecline denies the requested action.
	OptionDecline ApprovalOption = "decline"
	// OptionCancel denies the requested action and interrupts the current turn when supported.
	OptionCancel ApprovalOption = "cancel"
)

// ApprovalDecision is a normalized approval outcome from an ApprovalListener.
type ApprovalDecision struct {
	Choice ApprovalOption
}
