package claude

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/roomscript/coderoom/internal/agent"
)

// startWorkers starts the stdout and stderr readers, the approval worker and
// the exit monitor. The monitor waits for both readers to reach EOF before
// calling Wait, as os/exec requires. Once the process is gone, nothing can
// answer it, so the monitor also ends the approval worker.
func (c *Client) startWorkers() {
	c.lifecycle.readers.Add(2)
	go c.readStdout()
	go c.readStderr()
	go c.runApprovals()
	go c.stopOnCancel()
	go func() {
		c.lifecycle.readers.Wait()
		_ = c.proc.cmd.Wait()
		c.lifecycle.cancelFn()
		c.approvals.queue.close()
		c.queue.close()
		close(c.exited)
	}()
}

// stopOnCancel stops the process when the parent context ends. The approval
// worker exits with the lifecycle, so a running process would wait forever
// on its next permission request. It watches the parent, not the lifecycle
// context, which process exit also cancels.
//
// It may call Stop while Start is still in the handshake. That is safe,
// unlike an outside Stop during Start: the go statement in startWorkers
// comes after Start has set every field Stop reads.
func (c *Client) stopOnCancel() {
	select {
	case <-c.lifecycle.parent.Done():
		_ = c.Stop()
	case <-c.exited:
	}
}

func (c *Client) readStdout() {
	defer c.lifecycle.readers.Done()
	err := readLines(c.proc.stdout, func(line []byte) {
		c.obs.OnReceive(string(line))
		c.handleLine(line)
	})
	c.readFailed("stdout", err)
}

func (c *Client) readStderr() {
	defer c.lifecycle.readers.Done()
	err := readLines(c.proc.stderr, func(line []byte) {
		c.recordStderr(string(line))
		c.queue.push(logMessage(string(line)))
	})
	c.readFailed("stderr", err)
}

// readLines calls handle for each non-empty line, with no length limit: a
// single line can carry a whole file. It returns nil at EOF.
func readLines(r io.Reader, handle func([]byte)) error {
	br := bufio.NewReader(r)
	for {
		line, err := br.ReadBytes('\n')
		if line = bytes.TrimSuffix(line, []byte("\n")); len(line) > 0 {
			handle(line)
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read: %w", err)
		}
	}
}

// readFailed handles a reader that stopped before EOF. Claude would block
// on the unread pipe, so the process is killed and Read() returns an error
// instead of hanging. Errors after Stop come from closing the pipes.
func (c *Client) readFailed(stream string, err error) {
	if err == nil || c.lifecycle.ctx.Err() != nil {
		return
	}
	c.queue.push(logMessage(fmt.Sprintf("claude: %s %v; stopping the process", stream, err)))
	_ = c.proc.cmd.Process.Kill()
}

func (c *Client) recordStderr(line string) {
	c.stderrTail.mu.Lock()
	defer c.stderrTail.mu.Unlock()
	c.stderrTail.lines = append(c.stderrTail.lines, line)
	if len(c.stderrTail.lines) > stderrTailLines {
		c.stderrTail.lines = c.stderrTail.lines[1:]
	}
}

// handleLine processes one stdout line: control traffic, turn lifecycle, and
// the messages for Read().
func (c *Client) handleLine(line []byte) {
	d, err := c.dec.decode(line)
	if err != nil {
		c.queue.push(logMessage("claude: " + err.Error()))
		return
	}
	c.handleControl(d)
	switch d.signal {
	case signalInit:
		c.noteTurnStart()
	case signalReplay:
		c.notePromptReplayed()
	case signalNone, signalResult:
	}
	c.deliver(d)
	if d.signal == signalResult {
		c.endTurn(d.result)
	}
}

func (c *Client) handleControl(d decoded) {
	if d.controlResponse != nil {
		c.handleControlResponse(d.controlResponse)
	}
	if d.controlRequest != nil {
		c.handleControlRequest(d.controlRequest)
	}
	if d.controlCancel != nil {
		c.withdrawPermission(d.controlCancel.RequestID)
	}
}

func (c *Client) handleControlResponse(line *controlResponseLine) {
	if line.Response.RequestID != initRequestID {
		return
	}
	var err error
	if line.Response.Subtype == "error" {
		err = errors.New(line.Response.Error)
	}
	select {
	case c.initDone <- err:
	default:
	}
}

// handleControlRequest answers requests from Claude. Permission requests go
// to the approval worker; anything else is unsupported.
func (c *Client) handleControlRequest(line *controlRequestLine) {
	var tool canUseTool
	_ = json.Unmarshal(line.Request, &tool)
	if tool.Subtype != controlCanUseTool {
		_ = c.writeJSON(newControlError(line.RequestID, "unsupported control request: "+tool.Subtype))
		return
	}
	c.queuePermission(line.RequestID, tool)
}

// noteTurnStart marks a turn as started. Whose it is becomes known later.
func (c *Client) noteTurnStart() {
	c.turn.mu.Lock()
	defer c.turn.mu.Unlock()
	if c.turn.owner == ownerNone {
		c.turn.owner = ownerUnknown
	}
}

// notePromptReplayed claims the turn when Claude replays our prompt. Only
// our prompt can be pending, so the text is not compared: Claude may change
// it. A replay inside a turn Claude started on its own takes over that turn:
// the output so far is summarised, and items still open are dropped, so
// their remaining output is ignored instead of joining our turn.
func (c *Client) notePromptReplayed() {
	if !c.claimReplayedTurn() {
		return
	}
	c.dec.flushOpen()
	c.queue.push(logMessage(describeSelfTurn(c.selfTurnText.String(), resultLine{})))
	c.selfTurnText.Reset()
}

// claimReplayedTurn claims the turn for the pending prompt. It reports
// whether that took over a turn Claude started on its own.
func (c *Client) claimReplayedTurn() (tookOver bool) {
	c.turn.mu.Lock()
	defer c.turn.mu.Unlock()
	if c.turn.pending == nil {
		return false
	}
	tookOver = c.turn.owner == ownerSelf
	c.claimPromptLocked()
	return tookOver
}

// claimPromptLocked makes the running turn the answer to the pending
// prompt. c.turn.mu must be held.
func (c *Client) claimPromptLocked() {
	c.turn.owner = ownerPrompt
	if c.turn.pending.notice {
		c.turn.owner = ownerNotice
	}
	c.turn.pending = nil
	c.notice.reset()
}

// deliver queues messages for Read(). A notice turn's reply goes through
// the notice filter. A self-started turn's output is summarised at its end
// instead: the session drops stream fragments for idle participants. Model
// output before our prompt is replayed means Claude started the turn on its
// own. Any stream event counts, even one the decoder maps to nothing (an
// unmapped tool), so the turn's permission requests are denied.
func (c *Client) deliver(d decoded) {
	if len(d.msgs) == 0 && !d.streamed {
		return
	}
	msgs := d.msgs
	switch c.claimOutput() {
	case ownerSelf:
		for _, m := range msgs {
			if out, ok := m.Content.(agent.Output); ok {
				c.selfTurnText.WriteString(out.Text)
			}
		}
	case ownerNotice:
		c.queue.push(c.notice.filter(msgs)...)
	case ownerNone, ownerUnknown, ownerPrompt:
		c.queue.push(msgs...)
	}
}

func (c *Client) claimOutput() turnOwner {
	c.turn.mu.Lock()
	defer c.turn.mu.Unlock()
	if c.turn.owner == ownerUnknown {
		c.turn.owner = ownerSelf
		c.selfTurnText.Reset()
	}
	return c.turn.owner
}

// endTurn closes the turn and withdraws its pending approvals. The turn is
// marked over before the anchor flush is queued, so a Send issued in
// reaction to the flush is accepted.
func (c *Client) endTurn(result resultLine) {
	c.endApprovalTurn()
	owner := c.closeTurn()
	switch owner {
	case ownerPrompt, ownerNotice:
		anchor := turnStreamID
		if owner == ownerNotice {
			anchor = noticeTurnStreamID
			c.queue.push(c.notice.finish()...)
		}
		if result.IsError {
			c.queue.push(logMessage(describeTurnError(result)))
		}
		c.queue.push(agent.Message{StreamID: anchor, Mode: agent.ModeFlush, Content: agent.Output{}})
	case ownerSelf:
		c.queue.push(logMessage(describeSelfTurn(c.selfTurnText.String(), result)))
	case ownerNone, ownerUnknown:
		// A result with no turn in flight; nothing to close.
	}
}

// closeTurn ends the turn and returns its owner. A turn that ends before
// any replay or output is ours if our prompt is pending: a failure or a
// local slash command ends it without a replay. A result with no
// system/init counts as such a turn.
func (c *Client) closeTurn() turnOwner {
	c.turn.mu.Lock()
	defer c.turn.mu.Unlock()
	if c.turn.owner == ownerUnknown || c.turn.owner == ownerNone {
		if c.turn.pending != nil {
			c.claimPromptLocked()
		} else if c.turn.owner == ownerUnknown {
			c.turn.owner = ownerSelf
			c.selfTurnText.Reset()
		}
	}
	owner := c.turn.owner
	c.turn.owner = ownerNone
	return owner
}

func describeTurnError(r resultLine) string {
	if r.TerminalReason == "aborted_streaming" || r.TerminalReason == "aborted_tools" {
		return "claude: turn interrupted"
	}
	text := fmt.Sprintf("claude: turn failed (%s", r.Subtype)
	if r.TerminalReason != "" {
		text += ", " + r.TerminalReason
	}
	text += ")"
	if len(r.Errors) > 0 {
		text += ": " + strings.Join(r.Errors, "; ")
	}
	return text
}

func describeSelfTurn(text string, r resultLine) string {
	summary := strings.TrimSpace(text)
	if summary == "" {
		summary = strings.TrimSpace(r.Result)
	}
	return "claude: agent started a turn on its own: " + summary
}

func logMessage(text string) agent.Message {
	return agent.Message{StreamID: logStreamID, Mode: agent.ModeSingle, Content: agent.Log{Text: text}}
}
