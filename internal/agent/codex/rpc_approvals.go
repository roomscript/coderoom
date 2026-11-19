package codex

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/roomscript/coderoom/internal/agent"
)

func isApprovalRequest(msg rpcEnvelope) bool {
	if msg.ID == nil {
		return false
	}
	switch msg.Method {
	case methodCommandExecutionRequestApproval, methodFileChangeRequestApproval, methodPermissionsRequestApproval:
		return true
	default:
		return false
	}
}

func normalizeApproval(method string, params json.RawMessage) (agent.ApprovalRequest, approvalContext, error) {
	switch method {
	case methodCommandExecutionRequestApproval:
		req, err := normalizeCommandApproval(params)
		return req, approvalContext{kind: agent.ApprovalCommandExecution}, err
	case methodFileChangeRequestApproval:
		req, err := normalizeFileChangeApproval(params)
		return req, approvalContext{kind: agent.ApprovalFileChange}, err
	case methodPermissionsRequestApproval:
		req, perms, err := normalizePermissionsApproval(params)
		return req, approvalContext{kind: agent.ApprovalPermissions, perms: perms}, err
	default:
		return unknownApprovalRequest(method), approvalContext{kind: agent.ApprovalCommandExecution}, nil
	}
}

func unknownApprovalRequest(method string) agent.ApprovalRequest {
	return agent.ApprovalRequest{
		Presentation: agent.ApprovalPresentation{Summary: "Approval request: " + method},
		Options:      []agent.ApprovalOption{agent.OptionDecline},
	}
}

func normalizeCommandApproval(params json.RawMessage) (agent.ApprovalRequest, error) {
	var p commandExecutionRequestApprovalParams
	if err := json.Unmarshal(params, &p); err != nil {
		return agent.ApprovalRequest{}, fmt.Errorf("parse command approval params: %w", err)
	}
	return agent.ApprovalRequest{
		Kind:         agent.ApprovalCommandExecution,
		Details:      commandEvidence(p, params),
		Presentation: agent.ApprovalPresentation{Explanation: suppliedReason(p.Reason)},
		Options:      []agent.ApprovalOption{agent.OptionAccept, agent.OptionAcceptForSession, agent.OptionDecline, agent.OptionCancel},
	}, nil
}

func normalizeFileChangeApproval(params json.RawMessage) (agent.ApprovalRequest, error) {
	var p fileChangeRequestApprovalParams
	if err := json.Unmarshal(params, &p); err != nil {
		return agent.ApprovalRequest{}, fmt.Errorf("parse file change approval params: %w", err)
	}
	evidence := ""
	if p.GrantRoot != nil {
		evidence = "Grant root: " + *p.GrantRoot
	}
	return agent.ApprovalRequest{
		Kind: agent.ApprovalFileChange,
		// grantRoot is a permission expansion, not an exhaustive file target list.
		Details:      agent.ApprovalDetails{Backend: "codex", Tool: methodFileChangeRequestApproval, Raw: append(json.RawMessage(nil), params...), Operation: agent.FileOperation{Write: true}},
		Presentation: agent.ApprovalPresentation{Explanation: suppliedReason(p.Reason), Evidence: evidence},
		Options:      []agent.ApprovalOption{agent.OptionAccept, agent.OptionAcceptForSession, agent.OptionDecline, agent.OptionCancel},
	}, nil
}

func normalizePermissionsApproval(params json.RawMessage) (agent.ApprovalRequest, json.RawMessage, error) {
	var p permissionsRequestApprovalParams
	if err := json.Unmarshal(params, &p); err != nil {
		return agent.ApprovalRequest{}, nil, fmt.Errorf("parse permissions approval params: %w", err)
	}
	permsSummary := summarizePermissions(p.Permissions)
	summary := "Additional permissions"
	if permsSummary != "" {
		summary += ": " + permsSummary
	}
	return agent.ApprovalRequest{
		Kind:         agent.ApprovalPermissions,
		Details:      agent.ApprovalDetails{Backend: "codex", Tool: methodPermissionsRequestApproval, Raw: append(json.RawMessage(nil), params...)},
		Presentation: agent.ApprovalPresentation{Summary: summary, Explanation: suppliedReason(p.Reason), Evidence: permissionsDisplayEvidence(p)},
		Options:      []agent.ApprovalOption{agent.OptionAccept, agent.OptionDecline},
	}, p.Permissions, nil
}

func permissionsDisplayEvidence(p permissionsRequestApprovalParams) string {
	var text strings.Builder
	if p.Cwd != "" {
		fmt.Fprintf(&text, "Working directory: %s\n", p.Cwd)
	} else {
		text.WriteString("Working directory: unknown\n")
	}
	var scope bytes.Buffer
	if !isNullJSON(p.Permissions) && json.Indent(&scope, p.Permissions, "", "  ") == nil {
		fmt.Fprintf(&text, "Requested permissions:\n%s", scope.String())
	} else {
		text.WriteString("Requested permission scope unavailable.")
	}
	return text.String()
}

func commandEvidence(p commandExecutionRequestApprovalParams, raw json.RawMessage) agent.ApprovalDetails {
	command := agent.CommandOperation{Command: p.Command}
	if p.Cwd != nil {
		command.Cwd = *p.Cwd
	}
	return agent.ApprovalDetails{Backend: "codex", Tool: methodCommandExecutionRequestApproval, Raw: append(json.RawMessage(nil), raw...), Operation: command}
}

func suppliedReason(reason *string) string {
	if reason == nil {
		return ""
	}
	return *reason
}

func summarizePermissions(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var p requestPermissionProfile
	if err := json.Unmarshal(raw, &p); err != nil {
		return ""
	}
	var parts []string
	if !isNullJSON(p.FileSystem) {
		parts = append(parts, "filesystem")
	}
	if !isNullJSON(p.Network) {
		parts = append(parts, "network")
	}
	return strings.Join(parts, "+")
}
