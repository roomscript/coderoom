package claude

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/roomscript/coderoom/internal/agent"
)

const (
	// declineMessage is the deny message for a declined or cancelled tool
	// call.
	declineMessage = "The coderoom user declined this action."
	// askInReplyMessage denies AskUserQuestion, which is disallowed but
	// handled in case it still arrives.
	askInReplyMessage = "Ask the question in your reply; the user will answer in the room."
	// selfTurnMessage denies tools in a turn Claude started on its own: the
	// room sees the participant as idle, so nobody could judge the request.
	selfTurnMessage = "Tools are not available in a turn the user did not start."
)

var (
	allOptions    = []agent.ApprovalOption{agent.OptionAccept, agent.OptionAcceptForSession, agent.OptionDecline, agent.OptionCancel}
	acceptDecline = []agent.ApprovalOption{agent.OptionAccept, agent.OptionDecline}
)

// permissionRequest is a can_use_tool request waiting for a decision. ctx
// ends when Claude withdraws the request or its turn ends; the listener then
// stops waiting and no answer is sent.
type permissionRequest struct {
	ctx       context.Context //nolint:containedctx // per-request lifetime, handed to the worker with the request
	requestID string
	tool      canUseTool
}

// approvalTurn scopes pending requests to the turn that sent them. Stdout
// reader goroutine only.
type approvalTurn struct {
	ctx     context.Context //nolint:containedctx // parent of the turn's request contexts
	cancel  context.CancelFunc
	pending map[string]context.CancelFunc // by request_id
}

// newApprovalTurn opens the scope for the next turn's requests.
func (c *Client) newApprovalTurn() {
	ctx, cancel := context.WithCancel(c.lifecycle.ctx)
	c.approvals.turn = approvalTurn{ctx: ctx, cancel: cancel, pending: map[string]context.CancelFunc{}}
}

// endApprovalTurn withdraws the turn's requests that are still pending and
// opens the scope for the next turn.
func (c *Client) endApprovalTurn() {
	c.approvals.turn.cancel()
	c.approvals.denied.clear()
	c.newApprovalTurn()
}

// queuePermission answers what needs no decision and queues the rest for the
// approval worker, so the stdout reader never waits on a listener.
func (c *Client) queuePermission(requestID string, tool canUseTool) {
	if tool.ToolName == "AskUserQuestion" {
		_ = c.writeJSON(newControlSuccess(requestID, permissionDeny{Behavior: "deny", Message: askInReplyMessage}))
		return
	}
	if c.currentOwner() == ownerSelf {
		_ = c.writeJSON(newControlSuccess(requestID, permissionDeny{Behavior: "deny", Message: selfTurnMessage}))
		c.queue.push(logMessage("claude: denied " + tool.ToolName + " in a turn the agent started on its own"))
		return
	}
	ctx, cancel := context.WithCancel(c.approvals.turn.ctx)
	c.approvals.turn.pending[requestID] = cancel
	c.approvals.queue.push(permissionRequest{ctx: ctx, requestID: requestID, tool: tool})
}

// withdrawPermission handles control_cancel_request: Claude no longer waits
// for the answer.
func (c *Client) withdrawPermission(requestID string) {
	if cancel, ok := c.approvals.turn.pending[requestID]; ok {
		cancel()
		delete(c.approvals.turn.pending, requestID)
	}
}

// runApprovals answers queued permission requests one at a time, in order.
// Withdrawn requests are dropped without asking. It exits when the queue
// closes (process exit) or the client stops.
func (c *Client) runApprovals() {
	defer close(c.approvals.done)
	for {
		p, ok := c.approvals.queue.pop()
		if !ok || c.lifecycle.ctx.Err() != nil {
			return
		}
		if p.ctx.Err() != nil {
			continue
		}
		choice := c.decide(p.ctx, approvalRequestFor(p.tool))
		if p.ctx.Err() != nil {
			continue
		}
		c.answerPermission(p, choice)
	}
}

func (c *Client) answerPermission(p permissionRequest, choice agent.ApprovalOption) {
	resp := c.permissionResponse(p.tool, choice)
	if _, deny := resp.(permissionDeny); deny {
		// Recorded before the answer is written, so before the tool result.
		c.approvals.denied.add(p.tool.ToolUseID)
	}
	if err := c.writeJSON(newControlSuccess(p.requestID, resp)); err != nil && c.lifecycle.ctx.Err() == nil {
		c.queue.push(logMessage("claude: approval response failed: " + err.Error()))
	}
}

// decide asks the listener. Anything but a valid choice declines. A request
// withdrawn while the listener waits is not logged: nothing was declined.
func (c *Client) decide(ctx context.Context, req agent.ApprovalRequest) agent.ApprovalOption {
	l := c.approvals.listener
	if l == nil {
		c.queue.push(logMessage("claude: " + req.Ask + " (auto-decline: no approval listener configured)"))
		return agent.OptionDecline
	}
	decision, err := l.Decide(ctx, req)
	if err != nil {
		if ctx.Err() == nil {
			c.queue.push(logMessage(fmt.Sprintf("claude: %s (auto-decline: approval listener error: %v)", req.Ask, err)))
		}
		return agent.OptionDecline
	}
	if !slices.Contains(req.Options, decision.Choice) {
		c.queue.push(logMessage(fmt.Sprintf("claude: %s (auto-decline: invalid listener choice %q)", req.Ask, decision.Choice)))
		return agent.OptionDecline
	}
	return decision.Choice
}

// deniedTools holds the tool calls coderoom denied, so their results read as
// declined rather than failed. The tool result cannot tell: its text is our
// deny message, or Claude's own wording when the deny interrupts the turn.
// Written by the approval worker, read by the stdout reader.
type deniedTools struct {
	mu  sync.Mutex
	ids map[string]bool
}

func newDeniedTools() *deniedTools {
	return &deniedTools{ids: map[string]bool{}}
}

func (t *deniedTools) add(toolUseID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.ids[toolUseID] = true
}

// take reports whether the call was denied and forgets it.
func (t *deniedTools) take(toolUseID string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	denied := t.ids[toolUseID]
	delete(t.ids, toolUseID)
	return denied
}

func (t *deniedTools) clear() {
	t.mu.Lock()
	defer t.mu.Unlock()
	clear(t.ids)
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
