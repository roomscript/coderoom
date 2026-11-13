package agent

import (
	"context"
	"encoding/json"
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
	Kind ApprovalKind
	// Details retains operation data separately from the shortened display prompt.
	Details ApprovalDetails

	// Ask is intended to be rendered directly.
	Ask string

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
