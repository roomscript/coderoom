package claude

import (
	"encoding/json"
	"errors"
	"fmt"
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
	go func() {
		c.lifecycle.readers.Wait()
		_ = c.proc.cmd.Wait()
		c.lifecycle.cancelFn()
		c.approvals.queue.close()
		c.queue.close()
		close(c.exited)
	}()
}

func (c *Client) readStdout() {
	defer c.lifecycle.readers.Done()
	sc := newScanner(c.proc.stdout)
	for sc.Scan() {
		line := sc.Bytes()
		c.obs.OnReceive(string(line))
		c.handleLine(line)
	}
}

func (c *Client) readStderr() {
	defer c.lifecycle.readers.Done()
	sc := newScanner(c.proc.stderr)
	for sc.Scan() {
		text := sc.Text()
		c.recordStderr(text)
		c.queue.push(logMessage(text))
	}
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
	if d.controlResponse != nil {
		c.handleControlResponse(d.controlResponse)
	}
	if d.controlRequest != nil {
		c.handleControlRequest(d.controlRequest)
	}
	if d.signal == signalInit {
		c.noteTurnStart()
	}
	c.deliver(d.msgs)
	if d.signal == signalResult {
		c.endTurn(d.result)
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

// noteTurnStart marks a turn Claude started on its own: system/init with no
// turn in flight.
func (c *Client) noteTurnStart() {
	c.turn.mu.Lock()
	defer c.turn.mu.Unlock()
	if c.turn.kind == turnIdle {
		c.turn.kind = turnSelf
		c.selfTurnText.Reset()
	}
}

// deliver queues messages for Read(), except during a self-started turn,
// whose output is summarised at its end instead: the session drops stream
// fragments for idle participants.
func (c *Client) deliver(msgs []agent.Message) {
	if c.currentTurn() != turnSelf {
		c.queue.push(msgs...)
		return
	}
	for _, m := range msgs {
		if out, ok := m.Content.(agent.Output); ok {
			c.selfTurnText.WriteString(out.Text)
		}
	}
}

// endTurn closes the turn. The turn is marked idle before the anchor flush is
// queued, so a Send issued in reaction to the flush is accepted.
func (c *Client) endTurn(result resultLine) {
	switch c.swapTurn(turnIdle) {
	case turnPrompt:
		if result.IsError {
			c.queue.push(logMessage(describeTurnError(result)))
		}
		c.queue.push(agent.Message{StreamID: turnStreamID, Mode: agent.ModeFlush, Content: agent.Output{}})
	case turnSelf:
		c.queue.push(logMessage(describeSelfTurn(c.selfTurnText.String(), result)))
	case turnIdle:
		// A result with no turn in flight; nothing to close.
	}
}

func describeTurnError(r resultLine) string {
	if r.TerminalReason == "aborted_streaming" {
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
