package claude

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/roomscript/coderoom/internal/agent"
)

const (
	// declineMessage is the deny message for a declined or cancelled tool
	// call.
	declineMessage = "The coderoom user declined this action."
	// askInReplyMessage denies AskUserQuestion, which is disallowed but
	// handled in case it still arrives.
	askInReplyMessage = "Ask the question in your reply; the user will answer in the room."
)

var (
	allOptions    = []agent.ApprovalOption{agent.OptionAccept, agent.OptionAcceptForSession, agent.OptionDecline, agent.OptionCancel}
	acceptDecline = []agent.ApprovalOption{agent.OptionAccept, agent.OptionDecline}
)

// permissionRequest is a can_use_tool request waiting for a decision.
type permissionRequest struct {
	requestID string
	tool      canUseTool
}

// queuePermission answers what needs no decision and queues the rest for the
// approval worker, so the stdout reader never waits on a listener.
func (c *Client) queuePermission(requestID string, tool canUseTool) {
	if tool.ToolName == "AskUserQuestion" {
		_ = c.writeJSON(newControlSuccess(requestID, permissionDeny{Behavior: "deny", Message: askInReplyMessage}))
		return
	}
	c.approvals.queue.push(permissionRequest{requestID: requestID, tool: tool})
}

// runApprovals answers queued permission requests one at a time, in order.
// It exits when the queue closes (process exit) or the client stops.
func (c *Client) runApprovals() {
	defer close(c.approvals.done)
	for {
		p, ok := c.approvals.queue.pop()
		if !ok || c.lifecycle.ctx.Err() != nil {
			return
		}
		resp := c.permissionResponse(p.tool, c.decide(approvalRequestFor(p.tool)))
		if _, deny := resp.(permissionDeny); deny {
			c.dec.denied.add(p.tool.ToolUseID)
		}
		if err := c.writeJSON(newControlSuccess(p.requestID, resp)); err != nil && c.lifecycle.ctx.Err() == nil {
			c.queue.push(logMessage("claude: approval response failed: " + err.Error()))
		}
	}
}

// decide asks the listener. Anything but a valid choice declines.
func (c *Client) decide(req agent.ApprovalRequest) agent.ApprovalOption {
	l := c.approvals.listener
	if l == nil {
		c.queue.push(logMessage("claude: " + req.Ask + " (auto-decline: no approval listener configured)"))
		return agent.OptionDecline
	}
	decision, err := l.Decide(c.lifecycle.ctx, req)
	if err != nil {
		c.queue.push(logMessage(fmt.Sprintf("claude: %s (auto-decline: approval listener error: %v)", req.Ask, err)))
		return agent.OptionDecline
	}
	if !slices.Contains(req.Options, decision.Choice) {
		c.queue.push(logMessage(fmt.Sprintf("claude: %s (auto-decline: invalid listener choice %q)", req.Ask, decision.Choice)))
		return agent.OptionDecline
	}
	return decision.Choice
}

// permissionResponse maps a choice to the can_use_tool response.
func (c *Client) permissionResponse(tool canUseTool, choice agent.ApprovalOption) any {
	switch choice {
	case agent.OptionAccept:
		return permissionAllow{Behavior: "allow", UpdatedInput: inputOrEmpty(tool.Input)}
	case agent.OptionAcceptForSession:
		perms := sessionScoped(tool.PermissionSuggestions)
		if len(perms) == 0 {
			c.queue.push(logMessage("claude: no session rule offered for " + tool.ToolName + "; accepted once"))
		}
		return permissionAllow{Behavior: "allow", UpdatedInput: inputOrEmpty(tool.Input), UpdatedPermissions: perms}
	case agent.OptionCancel:
		return permissionDeny{Behavior: "deny", Message: declineMessage, Interrupt: true}
	default:
		return permissionDeny{Behavior: "deny", Message: declineMessage}
	}
}

// sessionScoped rewrites Claude's permission suggestions to last only for
// this process. Claude often suggests localSettings, which would write a
// rule into the user's project.
func sessionScoped(suggestions []json.RawMessage) []json.RawMessage {
	var out []json.RawMessage
	for _, s := range suggestions {
		var fields map[string]json.RawMessage
		if json.Unmarshal(s, &fields) != nil {
			continue
		}
		fields["destination"] = json.RawMessage(`"session"`)
		b, err := json.Marshal(fields)
		if err != nil {
			continue
		}
		out = append(out, b)
	}
	return out
}

func inputOrEmpty(input json.RawMessage) json.RawMessage {
	if isJSONNull(input) {
		return json.RawMessage(`{}`)
	}
	return input
}

// approvalRequestFor builds the user-facing request for a tool call.
func approvalRequestFor(tool canUseTool) agent.ApprovalRequest {
	switch tool.ToolName {
	case "Bash":
		var in bashInput
		_ = json.Unmarshal(tool.Input, &in)
		ask := "approve command execution: " + orUnknown(trimMiddle(in.Command, 160))
		if d := strings.TrimSpace(in.Description); d != "" {
			ask += " (" + trimMiddle(d, 120) + ")"
		}
		return agent.ApprovalRequest{Kind: agent.ApprovalCommandExecution, Ask: ask, Options: allOptions}
	case "Write", "Edit", "NotebookEdit":
		var in fileInput
		_ = json.Unmarshal(tool.Input, &in)
		path := in.FilePath
		if path == "" {
			path = in.NotebookPath
		}
		ask := "approve file changes: " + orUnknown(trimMiddle(path, 160))
		return agent.ApprovalRequest{Kind: agent.ApprovalFileChange, Ask: ask, Options: allOptions}
	case "ExitPlanMode":
		var in struct {
			Plan string `json:"plan"`
		}
		_ = json.Unmarshal(tool.Input, &in)
		ask := "approve plan: " + orUnknown(trimMiddle(firstLine(in.Plan), 160))
		return agent.ApprovalRequest{Kind: agent.ApprovalPermissions, Ask: ask, Options: acceptDecline}
	default:
		ask := "approve tool " + tool.ToolName + ": " + trimMiddle(string(tool.Input), 120)
		return agent.ApprovalRequest{Kind: agent.ApprovalPermissions, Ask: ask, Options: acceptDecline}
	}
}

func orUnknown(s string) string {
	if strings.TrimSpace(s) == "" {
		return "<unknown>"
	}
	return s
}

// firstLine returns the first non-empty line, without Markdown heading marks.
func firstLine(s string) string {
	for line := range strings.SplitSeq(s, "\n") {
		if line = strings.TrimSpace(strings.TrimLeft(line, "# ")); line != "" {
			return line
		}
	}
	return ""
}

// trimMiddle shortens s to maxLen runes, keeping its start and end.
func trimMiddle(s string, maxLen int) string {
	s = strings.TrimSpace(s)
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	head := maxLen / 2
	tail := maxLen - head - 1
	return string(runes[:head]) + "…" + string(runes[len(runes)-tail:])
}
