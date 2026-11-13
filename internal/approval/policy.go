// Package approval decides which requested operations need human approval.
// It never executes candidate commands or sends HTTP requests.
package approval

import (
	"context"

	"github.com/roomscript/coderoom/internal/agent"
)

// Outcome is a policy decision, independent of a backend's response options.
type Outcome string

const (
	// Review asks the human to decide.
	Review Outcome = "review"
	// Approve permits this operation once.
	Approve Outcome = "approve"
	// Decline refuses this operation.
	Decline Outcome = "decline"
)

// Result explains one decision without including private operation data.
type Result struct {
	Outcome        Outcome
	Reason         string
	ScannerVersion string
	ScannerConfig  string
}

// Policy contains only inputs used by the current rules.
type Policy struct {
	ProjectRoot string
	// Ignored checks project ignore rules; an error requires human review.
	Ignored func(context.Context, string) (bool, error)
	// Scanner defaults to the locally installed Gitleaks executable.
	Scanner Scanner
}

// Evaluate validates one typed operation without executing the candidate action.
func (p Policy) Evaluate(ctx context.Context, operation agent.Operation) Result {
	if ctx.Err() != nil {
		return review("policy.canceled")
	}
	switch op := operation.(type) {
	case agent.FileOperation:
		return p.evaluateFiles(ctx, op)
	case agent.CommandOperation:
		return p.evaluateCommand(ctx, op, 0)
	case agent.HTTPOperation:
		return p.evaluateHTTP(ctx, op)
	default:
		return review("policy.evidence_incomplete")
	}
}

func review(reason string) Result  { return Result{Outcome: Review, Reason: reason} }
func approve(reason string) Result { return Result{Outcome: Approve, Reason: reason} }
