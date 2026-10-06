package codex

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/roomscript/coderoom/internal/agent"
)

type noticeState uint8

type noticeTurnKind uint8

const (
	noticeTurnNone noticeTurnKind = iota
	noticeTurnDelivery
	noticeTurnKeepalive
)

const (
	noticeIdle      noticeState = iota
	noticePending               // SendNotice called; awaiting turn/started
	noticeActive                // turn/started received; awaiting first delta
	noticeBuffering             // first delta started with '{'; buffering until turn/completed
	noticeRelaying              // first delta did not start with '{'; relaying as reasoning
)

// noticeOutcome is returned by interceptNotice and its helpers.
type noticeOutcome uint8

type keepaliveActivityKind uint8

const (
	noticeUnhandled noticeOutcome = iota // not a notice turn; caller should process normally
	noticeContinue                       // handled; keep reading
	noticeExit                           // handled; exit worker (context cancelled)
)

const (
	keepalivePassive keepaliveActivityKind = iota
	keepaliveTool
	keepaliveUnknown
	keepaliveMalformed
)

type keepaliveActivity struct {
	kind     keepaliveActivityKind
	itemType string
}

// noticeContextPrefix is prepended to every notice prompt. It instructs the
// model to respond with only {"acknowledge":true} so the response can be
// silently discarded. Any JSON response containing "acknowledge":true is
// treated as compliant; extra fields are intentionally accepted.
const noticeContextPrefix = "[CONTEXT UPDATE — respond only with {\"acknowledge\":true}]\n\n"

const keepaliveNoticePrompt = "[MAINTENANCE — do not use tools; respond only with {\"acknowledge\":true}]"

// noticeAcknowledgementSchema constrains notice turns to a minimal response.
// It constrains the agent message to the acknowledgment shape at the Codex
// protocol level, complementing the prompt instruction for models that honour
// structured output.
var noticeAcknowledgementSchema = json.RawMessage(`{"type":"object","properties":{"acknowledge":{"type":"boolean","const":true}},"required":["acknowledge"],"additionalProperties":false}`)

// interceptNotice is called from handleStdoutEnvelope after turn state has
// been updated. noticeUnhandled means the caller should process the envelope
// normally; noticeContinue and noticeExit mean the filter took ownership.
func (c *Client) interceptNotice(ctx context.Context, msg rpcEnvelope) noticeOutcome {
	c.notice.mu.Lock()
	state := c.notice.state
	kind := c.notice.kind
	c.notice.mu.Unlock()

	if state == noticeIdle {
		return noticeUnhandled
	}
	return c.interceptActiveNotice(ctx, msg, kind)
}

func (c *Client) interceptActiveNotice(ctx context.Context, msg rpcEnvelope, kind noticeTurnKind) noticeOutcome {
	if kind == noticeTurnKeepalive {
		return c.interceptKeepalive(ctx, msg)
	}

	switch msg.Method {
	case methodTurnStarted:
		// noticePending transitions to noticeActive on the next turn/started.
		// No turn ID verification is needed: SendNotice enforces ErrTurnInProgress
		// so only one turn can be in flight at a time, meaning the next
		// turn/started is always ours.
		c.notice.mu.Lock()
		if c.notice.state == noticePending {
			c.notice.state = noticeActive
		}
		c.notice.mu.Unlock()
		return noticeContinue

	case methodAgentDelta, methodReasoningTextDelta, methodReasoningSummaryTextDelta:
		return c.handleNoticeDelta(ctx, msg)

	case methodReasoningSummaryPartAdded:
		// Boundary marker during a notice turn — part of the buffered or relayed
		// response; discard either way.
		return noticeContinue

	case methodItemCompleted:
		// Suppress item/completed for agentMessage items: the delta was intercepted
		// so no output stream was opened; forwarding the flush would produce a
		// spurious "stream not tracked" error. Non-agentMessage items pass through.
		return suppressNoticeAgentMessageItemCompleted(msg)

	case methodTurnCompleted:
		return c.handleNoticeCompleted(ctx)

	case methodTurnFailed:
		return c.handleNoticeFailed(ctx)

	default:
		// Approval requests and other protocol messages pass through unfiltered.
		return noticeUnhandled
	}
}

func (c *Client) handleNoticeDelta(ctx context.Context, msg rpcEnvelope) noticeOutcome {
	var p notificationParams
	if err := json.Unmarshal(msg.Params, &p); err != nil || p.Delta == "" {
		return noticeContinue
	}

	// Reasoning deltas (thinking summaries) are always silently discarded during
	// a notice turn. The {-heuristic and acknowledgment check only apply to the
	// agent message, so reasoning must not trigger the noticeRelaying path and
	// swallow subsequent agent message deltas.
	if isNoticeReasoningDelta(msg.Method) {
		return noticeContinue
	}

	c.notice.mu.Lock()
	state := c.notice.state

	switch state {
	case noticeActive:
		trimmed := strings.TrimLeft(p.Delta, " \t\r\n")
		if strings.HasPrefix(trimmed, "{") {
			c.notice.state = noticeBuffering
			c.notice.buf.WriteString(p.Delta)
			c.notice.mu.Unlock()
			return noticeContinue
		}
		c.notice.state = noticeRelaying
		c.notice.mu.Unlock()
		return relayDelta(ctx, c, p.Delta)

	case noticeBuffering:
		c.notice.buf.WriteString(p.Delta)
		c.notice.mu.Unlock()
		return noticeContinue

	case noticeRelaying:
		c.notice.mu.Unlock()
		return relayDelta(ctx, c, p.Delta)

	default:
		c.notice.mu.Unlock()
		return noticeUnhandled
	}
}

func isNoticeReasoningDelta(method string) bool {
	return method == methodReasoningTextDelta || method == methodReasoningSummaryTextDelta
}

func (c *Client) handleNoticeCompleted(ctx context.Context) noticeOutcome {
	c.notice.mu.Lock()
	state := c.notice.state
	buf := c.notice.buf.String()
	c.notice.state = noticeIdle
	c.notice.kind = noticeTurnNone
	c.notice.buf.Reset()
	c.notice.mu.Unlock()
	switch state {
	case noticeActive:
		// No deltas at all — treat as acknowledgment.
		return c.emitNoticeTurnFlush(ctx)

	case noticeBuffering:
		var r struct {
			Acknowledge bool `json:"acknowledge"`
		}
		if err := json.Unmarshal([]byte(buf), &r); err == nil && r.Acknowledge {
			return c.emitNoticeTurnFlush(ctx)
		}
		// Not acknowledged: replay as reasoning then flush both streams.
		if o := relayDelta(ctx, c, buf); o == noticeExit {
			return noticeExit
		}
		return relayAndTurnFlush(ctx, c)

	case noticeRelaying:
		// Flush reasoning stream then emit turn-level flush.
		return relayAndTurnFlush(ctx, c)

	default:
		return noticeUnhandled
	}
}

func (c *Client) emitKeepalive(ctx context.Context) noticeOutcome {
	return outcomeOf(sendBufMessage(ctx, c, readMessage{msg: agent.Message{
		StreamID: keepaliveStreamID,
		Mode:     agent.ModeSingle,
		Content:  agent.KeepAlive{},
	}}))
}

func (c *Client) emitNoticeTurnFlush(ctx context.Context) noticeOutcome {
	rm := readMessage{
		msg: agent.Message{
			StreamID: noticeTurnStreamID,
			Mode:     agent.ModeFlush,
			Content:  agent.Output{},
		},
	}
	return outcomeOf(sendBufMessage(ctx, c, rm))
}

// relayDelta emits a single reasoning-stream delta fragment.
func relayDelta(ctx context.Context, c *Client, text string) noticeOutcome {
	rm := readMessage{
		msg: agent.Message{
			StreamID: noticeRelayStreamID,
			Mode:     agent.ModeStream,
			Content:  agent.Reasoning{Text: text},
		},
	}
	return outcomeOf(sendBufMessage(ctx, c, rm))
}

// relayAndTurnFlush flushes the reasoning relay stream and then emits the
// turn-level output flush so the participant returns to idle.
func relayAndTurnFlush(ctx context.Context, c *Client) noticeOutcome {
	rm := readMessage{
		msg: agent.Message{
			StreamID: noticeRelayStreamID,
			Mode:     agent.ModeFlush,
			Content:  agent.Reasoning{},
		},
	}
	if o := outcomeOf(sendBufMessage(ctx, c, rm)); o == noticeExit {
		return noticeExit
	}
	rm = readMessage{
		msg: agent.Message{
			StreamID: noticeTurnStreamID,
			Mode:     agent.ModeFlush,
			Content:  agent.Output{},
		},
	}
	return outcomeOf(sendBufMessage(ctx, c, rm))
}

// handleNoticeFailed handles methodTurnFailed during a notice turn.
func (c *Client) handleNoticeFailed(ctx context.Context) noticeOutcome {
	c.notice.mu.Lock()
	prevState := c.notice.state
	c.notice.state = noticeIdle
	c.notice.kind = noticeTurnNone
	c.notice.buf.Reset()
	c.notice.mu.Unlock()
	if prevState == noticeRelaying {
		// Reasoning deltas were already emitted; flush reasoning stream then
		// emit a turn-level flush so the participant returns to idle.
		return relayAndTurnFlush(ctx, c)
	}
	return c.emitNoticeTurnFlush(ctx)
}

func (c *Client) interceptKeepalive(ctx context.Context, msg rpcEnvelope) noticeOutcome {
	switch msg.Method {
	case methodTurnStarted:
		return noticeContinue
	case methodTurnCompleted, methodTurnFailed:
		c.notice.mu.Lock()
		c.notice.state = noticeIdle
		c.notice.kind = noticeTurnNone
		c.notice.toolViolationReported = false
		c.notice.buf.Reset()
		c.notice.mu.Unlock()
		return c.emitKeepalive(ctx)
	default:
		if strings.HasPrefix(msg.Method, "item/") {
			activity := classifyKeepaliveActivity(msg)
			if activity.kind != keepalivePassive {
				return c.reportKeepaliveActivity(ctx, msg.Method, activity)
			}
			return noticeContinue
		}
		return noticeUnhandled
	}
}

func classifyKeepaliveActivity(msg rpcEnvelope) keepaliveActivity {
	switch msg.Method {
	case methodAgentDelta, methodReasoningTextDelta, methodReasoningSummaryTextDelta, methodReasoningSummaryPartAdded:
		return keepaliveActivity{kind: keepalivePassive}
	case methodItemStarted, methodItemCompleted:
		var p itemLifecycleParams
		if err := json.Unmarshal(msg.Params, &p); err != nil {
			return keepaliveActivity{kind: keepaliveMalformed}
		}
		var item itemKind
		if err := json.Unmarshal(p.Item, &item); err != nil {
			return keepaliveActivity{kind: keepaliveMalformed}
		}
		return classifyKeepaliveItem(item.Type)
	default:
		return keepaliveActivity{kind: keepaliveUnknown}
	}
}

func classifyKeepaliveItem(itemType string) keepaliveActivity {
	activity := keepaliveActivity{itemType: itemType}
	switch itemType {
	case "userMessage", "agentMessage", "reasoning":
		activity.kind = keepalivePassive
	case "commandExecution", "fileChange", "mcpToolCall", "dynamicToolCall",
		"collabAgentToolCall", "webSearch", "imageView", "imageGeneration":
		activity.kind = keepaliveTool
	case "":
		activity.kind = keepaliveMalformed
	default:
		activity.kind = keepaliveUnknown
	}
	return activity
}

func (c *Client) reportKeepaliveActivity(
	ctx context.Context,
	method string,
	activity keepaliveActivity,
) noticeOutcome {
	if !c.markToolViolationReported() {
		return noticeContinue
	}
	text := keepaliveActivityDiagnostic(method, activity)
	if err := c.Interrupt(); err != nil {
		text += "; interrupt failed: " + err.Error()
	}
	return outcomeOf(sendBufMessage(ctx, c, readMessage{msg: agent.Message{
		StreamID: logStreamID,
		Mode:     agent.ModeSingle,
		Content:  agent.Log{Text: text},
	}}))
}

func keepaliveActivityDiagnostic(method string, activity keepaliveActivity) string {
	switch activity.kind {
	case keepaliveTool:
		return "Keepalive interrupted: unexpected " + activity.itemType +
			" activity during maintenance (" + method + "). coderoom stopped the maintenance turn; normal participant work is unaffected."
	case keepaliveMalformed:
		return "Keepalive interrupted: malformed item lifecycle payload during maintenance (" + method +
			"). coderoom stopped the maintenance turn as a precaution; please report this event with the Codex version."
	default:
		item := activity.itemType
		if item == "" {
			item = "unknown"
		}
		return "Keepalive interrupted: unrecognized item type " + item +
			" during maintenance (" + method + "). coderoom stopped the maintenance turn as a precaution; please report this event with the Codex version."
	}
}

func (c *Client) markToolViolationReported() bool {
	c.notice.mu.Lock()
	defer c.notice.mu.Unlock()
	if c.notice.toolViolationReported {
		return false
	}
	c.notice.toolViolationReported = true
	return true
}

func suppressNoticeAgentMessageItemCompleted(msg rpcEnvelope) noticeOutcome {
	var p itemLifecycleParams
	if err := json.Unmarshal(msg.Params, &p); err != nil {
		return noticeUnhandled
	}
	var kind itemKind
	if err := json.Unmarshal(p.Item, &kind); err != nil {
		return noticeUnhandled
	}
	if kind.Type == "agentMessage" {
		return noticeContinue
	}
	return noticeUnhandled
}

// outcomeOf converts the bool returned by sendBufMessage into a noticeOutcome.
func outcomeOf(ok bool) noticeOutcome {
	if ok {
		return noticeContinue
	}
	return noticeExit
}
