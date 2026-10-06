package codex

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/roomscript/coderoom/internal/agent"
	"github.com/roomscript/coderoom/internal/linestream"
)

type workerFn = func(ctx context.Context, c *Client)

// readMessage carries the outcome of a single stdout notification.
type readMessage struct {
	msg agent.Message
	err error
}

type approvalContext struct {
	kind agent.ApprovalKind
	// perms holds the raw permissions payload from Codex, passed through verbatim
	// to the grant response on accept. Not interpreted by coderoom.
	perms json.RawMessage
}

type approvalRequest struct {
	rpcID       int
	req         agent.ApprovalRequest
	approvalCtx approvalContext
}

// readCodexErrWorker reads the Codex process stderr stream and emits each
// coalesced chunk as a log message on c.read.messages.
//
// This worker is long-lived and is expected to be started from Client.Start().
// It exits when ctx is canceled or when codex stderr closes.
func readCodexErrWorker(ctx context.Context, c *Client) {
	r := c.proc.codexErr
	defer func() { _ = r.Close() }()
	for chunk := range linestream.BatchReader(r) {
		rm := readMessage{
			msg: agent.Message{
				StreamID: stderrLogStreamID,
				Mode:     agent.ModeSingle,
				Content:  agent.Log{Text: sanitizeLogText(chunk)},
			},
		}
		select {
		case <-ctx.Done():
			return
		case c.read.messages <- rm:
		}
	}
}

// readCodexOut reads raw stdout lines, translates them to readMessage values,
// and sends meaningful ones on bufMessages.
func readCodexOutWorker(ctx context.Context, c *Client) {
	for {
		msg, err := rpcRead(c)
		if err != nil {
			shouldContinue := handleStdoutReadError(ctx, c, err)
			if shouldContinue {
				continue
			}
			return
		}
		if !handleStdoutEnvelope(ctx, c, msg) {
			return
		}
	}
}

func handleStdoutReadError(ctx context.Context, c *Client, err error) bool {
	if nonJSON, ok := isNonJSONStdoutLine(err); ok {
		readMsg := readMessage{
			msg: agent.Message{
				StreamID: logStreamID,
				Mode:     agent.ModeSingle,
				Content:  agent.Log{Text: sanitizeLogText(nonJSON.FormatLogLine())},
			},
		}
		return sendBufMessage(ctx, c, readMsg)
	}
	readMsg := readMessage{err: err}
	sendBufMessage(ctx, c, readMsg)
	return false
}

func handleStdoutEnvelope(ctx context.Context, c *Client, msg rpcEnvelope) bool {
	if shouldIgnoreStdoutEnvelope(msg) {
		return true
	}
	if handled, ok := handleTurnErrorEnvelope(ctx, c, msg); ok {
		return handled
	}
	if isApprovalRequest(msg) {
		if c.isKeepaliveTurn() {
			return declineKeepaliveApproval(ctx, c, msg)
		}
		return enqueueApprovalRequest(ctx, c, msg)
	}
	started := turnStartedFromEnvelope(msg)
	c.updateTurnState(msg.Method, started)
	switch c.interceptNotice(ctx, msg) {
	case noticeContinue:
		return true
	case noticeExit:
		return false
	case noticeUnhandled:
	}
	agentMsgs, err := messageFromEnvelope(msg)
	if err != nil {
		sendBufMessage(ctx, c, readMessage{err: err})
		return false
	}
	return sendAgentMessages(ctx, c, agentMsgs)
}

func (c *Client) isKeepaliveTurn() bool {
	c.notice.mu.Lock()
	defer c.notice.mu.Unlock()
	return c.notice.kind == noticeTurnKeepalive
}

func declineKeepaliveApproval(ctx context.Context, c *Client, msg rpcEnvelope) bool {
	_, approvalCtx, parseErr := normalizeApproval(msg.Method, msg.Params)
	responseErr := writeApprovalResult(c, *msg.ID, approvalCtx, agent.OptionDecline)
	text := "Keepalive interrupted: unexpected approval request during maintenance (" + msg.Method +
		"). coderoom auto-declined the request and stopped the maintenance turn; normal participant work is unaffected."
	if parseErr != nil {
		text += "; parse failed: " + parseErr.Error()
	}
	if responseErr != nil {
		text += "; response failed: " + responseErr.Error()
	}
	firstReport := c.markToolViolationReported()
	if firstReport {
		if err := c.Interrupt(); err != nil {
			text += "; interrupt failed: " + err.Error()
		}
	}
	if !firstReport {
		return true
	}
	return sendBufMessage(ctx, c, readMessage{msg: agent.Message{
		StreamID: logStreamID,
		Mode:     agent.ModeSingle,
		Content:  agent.Log{Text: text},
	}})
}

func shouldIgnoreStdoutEnvelope(msg rpcEnvelope) bool {
	if msg.ID != nil && msg.Method == "" {
		return true
	}
	if msg.Method == "" {
		return true
	}
	return false
}

func handleTurnErrorEnvelope(ctx context.Context, c *Client, msg rpcEnvelope) (bool, bool) {
	if !isTurnError(msg) {
		return false, false
	}
	logMsg, ok := turnErrorLogMessage(msg)
	if !ok {
		return true, true
	}
	return sendBufMessage(ctx, c, readMessage{msg: logMsg}), true
}

func turnStartedFromEnvelope(msg rpcEnvelope) *turnStartedParams {
	if msg.Method != methodTurnStarted {
		return nil
	}
	var p turnStartedParams
	if err := json.Unmarshal(msg.Params, &p); err != nil {
		return nil
	}
	return &p
}

func sendAgentMessages(ctx context.Context, c *Client, msgs []agent.Message) bool {
	for _, m := range msgs {
		if !sendBufMessage(ctx, c, readMessage{msg: m}) {
			return false
		}
	}
	return true
}

func sendBufMessage(ctx context.Context, c *Client, msg readMessage) bool {
	select {
	case c.read.bufMessages <- msg:
		return true
	case <-ctx.Done():
		return false
	}
}

// messageBufferWorker bridges the stdout pipe to read.messages via an unbounded internal
// buffer. Memory grows if the consumer falls behind; acceptable for a local
// CLI tool where stdout throughput is bounded by the agent's output rate.
//
// messageBufferWorker does not close read.messages directly because stderr also writes to
// it. Start() closes read.messages after both workers exit.
func messageBufferWorker(ctx context.Context, c *Client) {
	var buf []readMessage
	for {
		if len(buf) == 0 {
			select {
			case r, ok := <-c.read.bufMessages:
				if !ok {
					return
				}
				buf = append(buf, r)
			case <-ctx.Done():
				return
			}
		} else {
			select {
			case r, ok := <-c.read.bufMessages:
				if !ok {
					drainMessageBuffer(c, buf)
					return
				}
				buf = append(buf, r)
			case c.read.messages <- buf[0]:
				buf = buf[1:]
			case <-ctx.Done():
				drainMessageBuffer(c, buf)
				return
			}
		}
	}
}

func drainMessageBuffer(c *Client, buf []readMessage) {
	for _, pending := range buf {
		select {
		case c.read.messages <- pending:
		default:
		}
	}
}

func enqueueApprovalRequest(ctx context.Context, c *Client, msg rpcEnvelope) bool {
	req, approvalCtx, err := normalizeApproval(msg.Method, msg.Params)
	if err != nil {
		emitApprovalLog(ctx, c, "approval request parse failed; auto-declined")
		_ = writeApprovalResult(c, *msg.ID, approvalCtx, agent.OptionDecline)
		return true
	}
	ar := approvalRequest{rpcID: *msg.ID, req: req, approvalCtx: approvalCtx}
	select {
	case c.approvals.bufInbox <- ar:
		return true
	case <-ctx.Done():
		return false
	}
}

// approvalBufferWorker decouples stdout draining from human-paced approvals.
// The approval loop may block indefinitely waiting for a listener decision; this
// worker ensures readCodexOutWorker never blocks on that.
func approvalBufferWorker(ctx context.Context, c *Client) {
	var buf []approvalRequest
	for {
		if len(buf) == 0 {
			select {
			case r := <-c.approvals.bufInbox:
				buf = append(buf, r)
			case <-ctx.Done():
				// Intentionally do not drain buffered approvals on shutdown.
				//
				// Unlike agent output messages (delta/log/done), approvals are requests
				// that require a JSON-RPC response from a live Codex process. Once the
				// client is shutting down, forwarding buffered approvals is either
				// pointless (process already exiting) or harmful (it can extend shutdown
				// while waiting on human input). We stop promptly and allow the process
				// to exit; pending approvals are dropped.
				return
			}
		} else {
			select {
			case r := <-c.approvals.bufInbox:
				buf = append(buf, r)
			case c.approvals.inbox <- buf[0]:
				buf = buf[1:]
			case <-ctx.Done():
				// See comment above: do not drain approvals on shutdown.
				return
			}
		}
	}
}

func approvalLoopWorker(ctx context.Context, c *Client) {
	for {
		select {
		case <-ctx.Done():
			return
		case ar := <-c.approvals.inbox:
			if err := handleApprovalRequest(ctx, c, ar); err != nil {
				emitApprovalLog(ctx, c, fmt.Sprintf("approval handling failed: %v", err))
			}
		}
	}
}

func handleApprovalRequest(ctx context.Context, c *Client, approval approvalRequest) error {
	choice := decideApproval(ctx, c, approval.req, agent.OptionDecline)
	return writeApprovalResult(c, approval.rpcID, approval.approvalCtx, choice)
}

func writeApprovalResult(c *Client, rpcID int, approvalCtx approvalContext, choice agent.ApprovalOption) error {
	switch approvalCtx.kind {
	case agent.ApprovalCommandExecution, agent.ApprovalFileChange:
		return writeCommandDecisionResult(c, rpcID, choice)
	case agent.ApprovalPermissions:
		return writePermissionsGrantResult(c, rpcID, approvalCtx.perms, choice)
	default:
		return writeCommandDecisionResult(c, rpcID, agent.OptionDecline)
	}
}

func writeCommandDecisionResult(c *Client, rpcID int, choice agent.ApprovalOption) error {
	return rpcWriteResponse(c, rpcID, commandDecisionResult{Decision: string(choice)})
}

func writePermissionsGrantResult(c *Client, rpcID int, perms json.RawMessage, choice agent.ApprovalOption) error {
	if isNullJSON(perms) {
		perms = json.RawMessage("{}")
	}
	if choice == agent.OptionAccept {
		return rpcWriteResponse(c, rpcID, permissionsGrantResult{Permissions: perms})
	}
	return rpcWriteResponse(c, rpcID, permissionsGrantResult{Permissions: json.RawMessage("{}")})
}

func decideApproval(ctx context.Context, c *Client, req agent.ApprovalRequest, defaultChoice agent.ApprovalOption) agent.ApprovalOption {
	l := c.approvals.listener
	if l == nil {
		emitApprovalLog(ctx, c, fmt.Sprintf("%s (auto-decline: no approval listener configured)", req.Ask))
		return defaultChoice
	}
	decision, err := l.Decide(ctx, req)
	if err != nil {
		emitApprovalLog(ctx, c, fmt.Sprintf("%s (auto-decline: approval listener error: %v)", req.Ask, err))
		return defaultChoice
	}
	if !containsOption(req.Options, decision.Choice) {
		emitApprovalLog(ctx, c, fmt.Sprintf("%s (auto-decline: invalid listener choice %q)", req.Ask, decision.Choice))
		return defaultChoice
	}
	return decision.Choice
}

func containsOption(opts []agent.ApprovalOption, choice agent.ApprovalOption) bool {
	for _, opt := range opts {
		if opt == choice {
			return true
		}
	}
	return false
}

func emitApprovalLog(ctx context.Context, c *Client, text string) {
	msg := readMessage{
		msg: agent.Message{
			StreamID: logStreamID,
			Mode:     agent.ModeSingle,
			Content:  agent.Log{Text: sanitizeLogText("codex: " + text)},
		},
	}
	select {
	case c.read.messages <- msg:
	case <-ctx.Done():
	}
}
