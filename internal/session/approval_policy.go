package session

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/roomscript/coderoom/internal/agent"
	"github.com/roomscript/coderoom/internal/approval"
	"github.com/roomscript/coderoom/internal/participant"
)

func (s *Session) decideApproval(ctx context.Context, alias participant.Alias, req agent.ApprovalRequest, listener agent.ApprovalListener) (agent.ApprovalDecision, error) {
	if s.approvalPolicy == nil {
		decision, err := listener.Decide(ctx, req)
		if err != nil {
			return decision, fmt.Errorf("manual approval: %w", err)
		}
		return decision, nil
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(s.lifecycle.ctx, cancel)
	defer stop()
	p := s.approvalPolicy
	e := req.Details
	identity := approvalIdentity(e)
	result := p.Evaluate(ctx, projectOperation(e.Operation, p.ProjectRoot))
	if err := ctx.Err(); err != nil {
		return agent.ApprovalDecision{}, fmt.Errorf("approval evaluation: %w", err)
	}
	choice, automatic := policyApprovalChoice(result.Outcome)
	if automatic && !slices.Contains(req.Options, choice) {
		result = approval.Result{Outcome: approval.Review, Reason: "policy.choice_unavailable"}
		automatic = false
	}
	// Only explicit outcomes, static reasons and a request digest enter history.
	s.notify(AgentLog{Alias: alias, Text: fmt.Sprintf("approval policy v2 request=%s outcome=%s reason=%s scanner=%s config=%s", identity, result.Outcome, result.Reason, result.ScannerVersion, result.ScannerConfig)})
	if err := ctx.Err(); err != nil {
		return agent.ApprovalDecision{}, fmt.Errorf("approval response: %w", err)
	}
	if automatic {
		return agent.ApprovalDecision{Choice: choice}, nil
	}
	return s.requestManualApproval(ctx, alias, identity, req, listener)
}

func approvalIdentity(e agent.ApprovalDetails) string {
	raw := e.Raw
	e.Raw = nil
	hash := sha256.New()
	// The structured fields contain only JSON-supported values. Keep the raw
	// backend bytes separate so even unknown/malformed evidence is correlated.
	_ = json.NewEncoder(hash).Encode(e)
	_, _ = hash.Write(raw)
	return fmt.Sprintf("%x", hash.Sum(nil))
}

func (s *Session) requestManualApproval(ctx context.Context, alias participant.Alias, identity string, req agent.ApprovalRequest, listener agent.ApprovalListener) (agent.ApprovalDecision, error) {
	decision, err := listener.Decide(ctx, req)
	if err == nil {
		s.notify(AgentLog{Alias: alias, Text: fmt.Sprintf("approval policy v2 request=%s origin=human decision=%q", identity, decision.Choice)})
	}
	if err != nil {
		return decision, fmt.Errorf("manual approval: %w", err)
	}
	return decision, nil
}

func projectOperation(operation agent.Operation, root string) agent.Operation {
	if file, ok := operation.(agent.FileOperation); ok && file.Cwd == "" && len(file.Paths) > 0 {
		file.Cwd = root
		return file
	}
	return operation
}

// Session translates policy outcomes into the backend's response vocabulary.
func policyApprovalChoice(outcome approval.Outcome) (agent.ApprovalOption, bool) {
	switch outcome {
	case approval.Approve:
		return agent.OptionAccept, true
	case approval.Decline:
		return agent.OptionDecline, true
	default:
		return "", false
	}
}
