package codex

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

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
		Ask:     "approve request (unknown method): " + method,
		Options: []agent.ApprovalOption{agent.OptionDecline},
	}
}

func normalizeCommandApproval(params json.RawMessage) (agent.ApprovalRequest, error) {
	var p commandExecutionRequestApprovalParams
	if err := json.Unmarshal(params, &p); err != nil {
		return agent.ApprovalRequest{}, fmt.Errorf("parse command approval params: %w", err)
	}
	cmd := strings.TrimSpace(p.Command)
	if cmd == "" {
		cmd = "<unknown>"
	}
	cmd = trimMiddle(cmd, 160)
	ask := "approve command execution: " + cmd
	if p.Cwd != nil && strings.TrimSpace(*p.Cwd) != "" {
		ask += " (cwd: " + trimMiddle(strings.TrimSpace(*p.Cwd), 80) + ")"
	}
	return agent.ApprovalRequest{
		Kind:    agent.ApprovalCommandExecution,
		Ask:     ask,
		Options: []agent.ApprovalOption{agent.OptionAccept, agent.OptionAcceptForSession, agent.OptionDecline, agent.OptionCancel},
	}, nil
}

func normalizeFileChangeApproval(params json.RawMessage) (agent.ApprovalRequest, error) {
	var p fileChangeRequestApprovalParams
	if err := json.Unmarshal(params, &p); err != nil {
		return agent.ApprovalRequest{}, fmt.Errorf("parse file change approval params: %w", err)
	}
	ask := "approve file changes"
	if p.GrantRoot != nil && strings.TrimSpace(*p.GrantRoot) != "" {
		ask += " (grantRoot: " + trimMiddle(strings.TrimSpace(*p.GrantRoot), 80) + ")"
	}
	if p.Reason != nil && strings.TrimSpace(*p.Reason) != "" {
		ask += " — " + trimMiddle(strings.TrimSpace(*p.Reason), 120)
	}
	return agent.ApprovalRequest{
		Kind:    agent.ApprovalFileChange,
		Ask:     ask,
		Options: []agent.ApprovalOption{agent.OptionAccept, agent.OptionAcceptForSession, agent.OptionDecline, agent.OptionCancel},
	}, nil
}

func normalizePermissionsApproval(params json.RawMessage) (agent.ApprovalRequest, json.RawMessage, error) {
	var p permissionsRequestApprovalParams
	if err := json.Unmarshal(params, &p); err != nil {
		return agent.ApprovalRequest{}, nil, fmt.Errorf("parse permissions approval params: %w", err)
	}
	permsSummary := summarizePermissions(p.Permissions)
	ask := "approve additional permissions"
	if permsSummary != "" {
		ask += ": " + permsSummary
	}
	if strings.TrimSpace(p.Cwd) != "" {
		ask += " (cwd: " + trimMiddle(strings.TrimSpace(p.Cwd), 80) + ")"
	}
	if p.Reason != nil && strings.TrimSpace(*p.Reason) != "" {
		ask += " — " + trimMiddle(strings.TrimSpace(*p.Reason), 120)
	}
	return agent.ApprovalRequest{
		Kind:    agent.ApprovalPermissions,
		Ask:     ask,
		Options: []agent.ApprovalOption{agent.OptionAccept, agent.OptionDecline},
	}, p.Permissions, nil
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

func trimMiddle(s string, maxLen int) string {
	if maxLen <= 0 || utf8.RuneCountInString(s) <= maxLen {
		return s
	}
	if maxLen <= 1 {
		runes := []rune(s)
		return string(runes[:1])
	}
	runes := []rune(s)
	head := maxLen / 2
	tail := maxLen - head - 1
	return string(runes[:head]) + "…" + string(runes[len(runes)-tail:])
}
