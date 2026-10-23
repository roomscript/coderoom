package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/roomscript/coderoom/internal/agent"
)

const modelsStreamID agent.StreamID = "codex:models"

type modelChange struct {
	err         error
	interrupted bool
}

var _ agent.ModelSelector = (*Client)(nil)

// Settings returns the last established thread settings, without I/O.
func (c *Client) Settings() agent.Settings {
	c.turn.mu.Lock()
	defer c.turn.mu.Unlock()
	settings := c.models.settings
	settings.Backend = c.Name()
	return settings
}

// SelectModel starts a guarded acknowledgement turn followed by settings readback.
func (c *Client) SelectModel(opt agent.ModelOption, effort string) error {
	if !opt.SupportsEffort(effort) {
		return fmt.Errorf("codex: model %s does not support effort %q", opt.Name, effort)
	}
	if err := c.admitModelChange(); err != nil {
		return err
	}
	c.turn.mu.Lock()
	threadID := c.turn.threadID
	c.turn.mu.Unlock()
	err := rpcWriteHandled(c, methodTurnStart, turnStartParams{
		ThreadID: threadID, Model: opt.ID, Effort: effort,
		Input:        []turnInput{{Type: "text", Text: keepaliveNoticePrompt}},
		OutputSchema: noticeAcknowledgementSchema,
	}, func(_ json.RawMessage, err error) {
		if err != nil {
			c.readModelSettings(err)
		}
	})
	if err != nil {
		c.turn.mu.Lock()
		c.models.change = nil
		c.turn.state = turnState{kind: turnIdle}
		c.turn.mu.Unlock()
	}
	return err
}

func (c *Client) admitModelChange() error {
	c.turn.mu.Lock()
	defer c.turn.mu.Unlock()
	if c.turn.threadID == "" {
		return errors.New("codex: thread not started")
	}
	if c.turn.state.kind != turnIdle {
		return agent.ErrTurnInProgress
	}
	c.models.change = &modelChange{}
	c.turn.state = turnState{kind: turnInflightUnknownID}
	return nil
}

func (c *Client) handleModelEnvelope(ctx context.Context, msg rpcEnvelope) (bool, bool) {
	c.turn.mu.Lock()
	change := c.models.change
	threadID := c.turn.threadID
	readingSettings := c.turn.state.kind == turnModelReadback
	c.turn.mu.Unlock()
	if change == nil {
		return false, false
	}
	if msg.ID != nil && msg.Method != "" {
		return true, c.handleModelRequest(ctx, change, readingSettings, msg)
	}
	if !isModelNotification(msg.Method) {
		return false, false
	}
	if readingSettings || isForeignThread(msg, threadID) {
		return true, ctx.Err() == nil
	}
	c.handleModelNotification(change, msg)

	return true, ctx.Err() == nil
}

func (c *Client) interruptModelActivity(change *modelChange, err error) {
	// Only the stdout worker mutates the active exchange after admission.
	if change.interrupted {
		return
	}
	change.interrupted = true
	change.err = errors.Join(change.err, err)
	change.err = errors.Join(change.err, c.Interrupt())
}

func modelConfirmationError(msg rpcEnvelope) error {
	var p turnCompletedParams
	if err := json.Unmarshal(msg.Params, &p); err != nil {
		return fmt.Errorf("decode model confirmation: %w", err)
	}
	if p.Turn.Error != nil {
		return errors.New(formatCodexTurnErrorLog(*p.Turn.Error, false))
	}
	if msg.Method == methodTurnFailed || p.Turn.Status != "completed" {
		return fmt.Errorf("codex: model confirmation ended with status %q", p.Turn.Status)
	}
	return nil
}

func (c *Client) readModelSettings(changeErr error) {
	c.turn.mu.Lock()
	change := c.models.change
	if change == nil || c.turn.state.kind == turnModelReadback {
		c.turn.mu.Unlock()
		return
	}
	c.turn.state = turnState{kind: turnModelReadback}
	threadID := c.turn.threadID
	c.turn.mu.Unlock()
	err := rpcWriteHandled(c, "thread/read", map[string]any{"threadId": threadID}, func(raw json.RawMessage, err error) {
		settings, readErr := parseModelSettings(raw, err)
		c.finishModelChange(settings, errors.Join(changeErr, readErr))
	})
	if err != nil {
		c.finishModelChange(agent.Settings{Backend: c.Name()}, errors.Join(changeErr, err))
	}
}

func parseModelSettings(raw json.RawMessage, rpcErr error) (agent.Settings, error) {
	settings := agent.Settings{Backend: "codex"}
	if rpcErr != nil {
		return settings, rpcErr
	}
	var result struct {
		Thread *struct {
			Model  string `json:"model"`
			Effort string `json:"reasoningEffort"`
		} `json:"thread"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return settings, fmt.Errorf("decode thread settings: %w", err)
	}
	if result.Thread == nil {
		return settings, errors.New("codex: settings response has no thread")
	}
	settings.Model, settings.Effort = result.Thread.Model, result.Thread.Effort
	return settings, nil
}

func (c *Client) finishModelChange(settings agent.Settings, err error) {
	c.turn.mu.Lock()
	c.models.settings = settings
	c.models.change = nil
	c.turn.state = turnState{kind: turnIdle}
	c.turn.mu.Unlock()
	sendBufMessage(c.lifecycle.ctx, c, readMessage{msg: agent.Message{
		StreamID: modelsStreamID, Mode: agent.ModeSingle,
		Content: agent.ModelSelected{Settings: settings, Err: err},
	}})
}

func isForeignThread(msg rpcEnvelope, threadID string) bool {
	var scope struct {
		ThreadID string `json:"threadId"`
	}
	_ = json.Unmarshal(msg.Params, &scope)
	return scope.ThreadID != "" && scope.ThreadID != threadID
}

func (c *Client) handleModelNotification(change *modelChange, msg rpcEnvelope) {
	switch msg.Method {
	case methodTurnStarted:
		c.updateTurnState(msg.Method, turnStartedFromEnvelope(msg))
	case methodTurnCompleted, methodTurnFailed:
		outcomeErr := change.err
		if outcomeErr == nil {
			outcomeErr = modelConfirmationError(msg)
		}
		c.readModelSettings(outcomeErr)
	case methodError:
		var p errorNotificationParams
		if json.Unmarshal(msg.Params, &p) == nil && !p.WillRetry && p.Error != nil {
			change.err = errors.Join(change.err, errors.New(formatCodexTurnErrorLog(*p.Error, false)))
		}
	default:
		c.guardModelActivity(change, msg)
	}
}

func (c *Client) guardModelActivity(change *modelChange, msg rpcEnvelope) {
	if classifyKeepaliveActivity(msg).kind != keepalivePassive {
		c.interruptModelActivity(change, fmt.Errorf("codex: unexpected activity during model confirmation: %s", msg.Method))
	}
}

func isModelNotification(method string) bool {
	switch method {
	case methodTurnStarted, methodTurnCompleted, methodTurnFailed, methodError:
		return true
	default:
		return strings.HasPrefix(method, "item/")
	}
}

func (c *Client) handleModelRequest(ctx context.Context, change *modelChange, readingSettings bool, msg rpcEnvelope) bool {
	if isApprovalRequest(msg) {
		_, approvalCtx, parseErr := normalizeApproval(msg.Method, msg.Params)
		responseErr := writeApprovalResult(c, *msg.ID, approvalCtx, agent.OptionDecline)
		if !readingSettings {
			c.interruptModelActivity(change, errors.Join(errors.New("codex: model confirmation requested approval"), parseErr, responseErr))
		}
		return c.reportModelReplyError(ctx, responseErr)
	}
	err := rpcWriteError(c, *msg.ID, -32601, "Unsupported request during model selection: "+msg.Method)
	if !readingSettings && strings.HasPrefix(msg.Method, "item/") {
		c.guardModelActivity(change, msg)
	}
	return c.reportModelReplyError(ctx, err)
}

func (c *Client) reportModelReplyError(ctx context.Context, err error) bool {
	if err == nil {
		return ctx.Err() == nil
	}
	sendBufMessage(ctx, c, readMessage{err: err})
	return false
}
