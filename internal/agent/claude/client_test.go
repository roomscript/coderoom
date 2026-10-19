package claude

import (
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/roomscript/coderoom/internal/agent"
)

const lineInitOK = `{"type":"control_response","response":{"subtype":"success","request_id":"coderoom-init","response":{}}}`

// peerScript returns a shell script for a fake claude process. Each step
// reads one line from stdin, then prints its lines. After the last step the
// peer drains stdin, so it exits when the client closes stdin. A test that
// must act before the peer goes on starts a new step and then writes a line,
// usually with Interrupt.
func peerScript(steps ...[]string) string {
	var sb strings.Builder
	for _, step := range steps {
		sb.WriteString("read -r _; ")
		for _, l := range step {
			sb.WriteString("printf '%s\\n' '" + l + "'; ")
		}
	}
	sb.WriteString("cat >/dev/null")
	return sb.String()
}

// startPeer starts a Client against a fake peer and stops it at test end.
func startPeer(t *testing.T, script string, opts ...Option) *Client {
	t.Helper()
	opts = append([]Option{WithCommand("sh", "-c", script)}, opts...)
	c := New(t.TempDir(), opts...)
	if err := c.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = c.Stop() })
	return c
}

// waitClosed fails the test if ch is not closed in time.
func waitClosed(t *testing.T, ch <-chan struct{}, failure string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal(failure)
	}
}

// readUntil reads messages until stop returns true, failing after a timeout.
func readUntil(t *testing.T, c *Client, stop func(agent.Message) bool) []agent.Message {
	t.Helper()
	type result struct {
		msgs []agent.Message
		err  error
	}
	done := make(chan result, 1)
	go func() {
		var msgs []agent.Message
		for {
			m, err := c.Read()
			if err != nil {
				done <- result{msgs, err}
				return
			}
			msgs = append(msgs, m)
			if stop(m) {
				done <- result{msgs, nil}
				return
			}
		}
	}()
	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("Read: %v (after %+v)", r.err, r.msgs)
		}
		return r.msgs
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for message")
		return nil
	}
}

// readUntilExit reads messages until Read reports the process exited,
// failing after a timeout.
func readUntilExit(t *testing.T, c *Client) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			if _, err := c.Read(); err != nil {
				return
			}
		}
	}()
	waitClosed(t, done, "Read did not report the process exit")
}

func isAnchorFlush(m agent.Message) bool {
	return m.StreamID == turnStreamID && m.Mode == agent.ModeFlush
}

func isLog(m agent.Message) bool {
	_, ok := m.Content.(agent.Log)
	return ok
}

// recordingObserver keeps every line sent to the peer.
type recordingObserver struct {
	mu   sync.Mutex
	sent []string
}

func (o *recordingObserver) OnSend(msg string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.sent = append(o.sent, msg)
}

func (o *recordingObserver) OnReceive(string) {}

func (o *recordingObserver) sentLines() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return slices.Clone(o.sent)
}

func TestStart_handshake(t *testing.T) {
	obs := &recordingObserver{}
	startPeer(t, peerScript([]string{lineInitOK}), WithObserver(obs))

	sent := obs.sentLines()
	if len(sent) != 1 {
		t.Fatalf("sent %d lines, want the initialize request", len(sent))
	}
	var req struct {
		Type      string `json:"type"`
		RequestID string `json:"request_id"`
		Request   struct {
			Subtype string `json:"subtype"`
		} `json:"request"`
	}
	if err := json.Unmarshal([]byte(sent[0]), &req); err != nil {
		t.Fatal(err)
	}
	if req.Type != "control_request" || req.RequestID != initRequestID || req.Request.Subtype != "initialize" {
		t.Errorf("initialize request = %+v", req)
	}
}

func TestStart_failures(t *testing.T) {
	tests := []struct {
		name   string
		script string
	}{
		{"process exits", "echo 'Not logged in' >&2; exit 1"},
		{"error response", peerScript([]string{
			`{"type":"control_response","response":{"subtype":"error","request_id":"coderoom-init","error":"bad"}}`,
		})},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := New(t.TempDir(), WithCommand("sh", "-c", tt.script))
			if err := c.Start(); err == nil {
				_ = c.Stop()
				t.Fatal("expected error")
			}
		})
	}
}

func TestStart_timeout(t *testing.T) {
	c := New(t.TempDir(), WithCommand("sh", "-c", "cat >/dev/null"))
	c.startTimeout = 50 * time.Millisecond
	if err := c.Start(); err == nil {
		_ = c.Stop()
		t.Fatal("expected error")
	}
}

func TestSend_turn(t *testing.T) {
	c := startPeer(t, peerScript(
		[]string{lineInitOK},
		[]string{lineSystemInit, replayLine("Reply with just: ok"), lineMessageStart, lineTextStart, textDeltaLine("ok"), lineBlockStop, lineResultOK},
	))

	anchor, err := c.Send("Reply with just: ok")
	if err != nil {
		t.Fatal(err)
	}
	if anchor != turnStreamID {
		t.Errorf("anchor = %q, want %q", anchor, turnStreamID)
	}
	msgs := readUntil(t, c, isAnchorFlush)
	if out, _ := accumulate(t, msgs[:len(msgs)-1]).Content.(agent.Output); out.Text != "ok" {
		t.Errorf("output = %q, want ok", out.Text)
	}
}

func TestSend_turnInProgress(t *testing.T) {
	c := startPeer(t, peerScript(
		[]string{lineInitOK},
		[]string{lineSystemInit, replayLine("first")},
		// The turn ends only after the test interrupts it.
		[]string{lineResultOK},
	))

	if _, err := c.Send("first"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Send("second"); !errors.Is(err, agent.ErrTurnInProgress) {
		t.Errorf("second Send error = %v, want ErrTurnInProgress", err)
	}
	if err := c.Interrupt(); err != nil {
		t.Fatal(err)
	}
	readUntil(t, c, isAnchorFlush)
	if _, err := c.Send("after the turn"); err != nil {
		t.Fatalf("Send after turn end: %v", err)
	}
}

func TestSend_beforeStart(t *testing.T) {
	if _, err := New(t.TempDir()).Send("hi"); err == nil {
		t.Fatal("expected error")
	}
}

// The turn fails before Claude replays the prompt, as an API error can.
func TestRead_turnErrorLoggedBeforeAnchorFlush(t *testing.T) {
	failed := `{"type":"result","subtype":"error_during_execution","is_error":true,"terminal_reason":"api_error","errors":["overloaded"]}`
	c := startPeer(t, peerScript([]string{lineInitOK}, []string{lineSystemInit, failed}))

	if _, err := c.Send("hi"); err != nil {
		t.Fatal(err)
	}
	msgs := readUntil(t, c, isAnchorFlush)
	if len(msgs) != 2 || !isLog(msgs[0]) {
		t.Fatalf("messages = %+v, want a log then the anchor flush", msgs)
	}
}

func TestRead_stderrAsLog(t *testing.T) {
	c := startPeer(t, "read -r _; echo 'warning: something' >&2; printf '%s\\n' '"+lineInitOK+"'; cat >/dev/null")

	msgs := readUntil(t, c, isLog)
	if log, _ := msgs[len(msgs)-1].Content.(agent.Log); log.Text != "warning: something" {
		t.Errorf("log = %q", log.Text)
	}
}

func TestRead_selfStartedTurnSummarisedAsLog(t *testing.T) {
	// After the handshake the peer starts a turn on its own (as a finished
	// background task or a cron prompt would), then waits for the test prompt.
	script := "read -r _; printf '%s\\n' '" + lineInitOK + "' '" + lineSystemInit + "' '" + lineMessageStart + "' '" +
		lineTextStart + "' '" + textDeltaLine("tick") + "' '" + lineBlockStop + "' '" + lineResultOK + "'; cat >/dev/null"
	c := startPeer(t, script)

	msgs := readUntil(t, c, isLog)
	if len(msgs) != 1 {
		t.Fatalf("messages = %+v, want only the summary log", msgs)
	}
	if log, _ := msgs[0].Content.(agent.Log); !strings.Contains(log.Text, "tick") {
		t.Errorf("summary = %q, want it to include the turn output", log.Text)
	}
	if _, err := c.Send("hi"); err != nil {
		t.Fatalf("Send after self-started turn: %v", err)
	}
}

func TestRead_selfStartedTurnAfterSend(t *testing.T) {
	// Claude starts a turn of its own just after Send, then answers the
	// prompt. Only the second turn is the reply.
	c := startPeer(t, peerScript(
		[]string{lineInitOK},
		[]string{
			lineSystemInit, lineMessageStart, lineTextStart, textDeltaLine("tick"), lineBlockStop, lineResultOK,
			lineSystemInit, replayLine("hi"), lineMessageStart, lineTextStart, textDeltaLine("ok"), lineBlockStop, lineResultOK,
		},
	))

	if _, err := c.Send("hi"); err != nil {
		t.Fatal(err)
	}
	msgs := readUntil(t, c, isAnchorFlush)
	if !isLog(msgs[0]) || !strings.Contains(msgs[0].Content.(agent.Log).Text, "tick") {
		t.Fatalf("first message = %+v, want the self-started turn summary", msgs[0])
	}
	if out, _ := accumulate(t, msgs[1:len(msgs)-1]).Content.(agent.Output); out.Text != "ok" {
		t.Errorf("reply = %q, want ok", out.Text)
	}
	if _, err := c.Send("next"); err != nil {
		t.Errorf("Send after the reply: %v", err)
	}
}

func TestRead_replayClaimsTurnWhateverItsText(t *testing.T) {
	c := startPeer(t, peerScript(
		[]string{lineInitOK},
		[]string{lineSystemInit, replayLine("hi, reworded"), lineMessageStart, lineTextStart, textDeltaLine("ok"), lineBlockStop, lineResultOK},
	))

	if _, err := c.Send("hi"); err != nil {
		t.Fatal(err)
	}
	msgs := readUntil(t, c, isAnchorFlush)
	if out, _ := accumulate(t, msgs[:len(msgs)-1]).Content.(agent.Output); out.Text != "ok" {
		t.Errorf("reply = %q, want ok", out.Text)
	}
	if _, err := c.Send("next"); err != nil {
		t.Errorf("Send after the reply: %v", err)
	}
}

func TestRead_replayDuringSelfStartedTurn(t *testing.T) {
	// Claude takes the prompt into a turn it started on its own. Output
	// before the replay is the self-started part.
	c := startPeer(t, peerScript(
		[]string{lineInitOK},
		[]string{
			lineSystemInit, lineMessageStart, lineTextStart, textDeltaLine("tick"), lineBlockStop,
			replayLine("hi"), lineMessageStart, lineTextStart, textDeltaLine("ok"), lineBlockStop, lineResultOK,
		},
	))

	if _, err := c.Send("hi"); err != nil {
		t.Fatal(err)
	}
	msgs := readUntil(t, c, isAnchorFlush)
	if !isLog(msgs[0]) || !strings.Contains(msgs[0].Content.(agent.Log).Text, "tick") {
		t.Fatalf("first message = %+v, want the self-started part as a log", msgs[0])
	}
	if out, _ := accumulate(t, msgs[1:len(msgs)-1]).Content.(agent.Output); out.Text != "ok" {
		t.Errorf("reply = %q, want ok", out.Text)
	}
	if _, err := c.Send("next"); err != nil {
		t.Errorf("Send after the reply: %v", err)
	}
}

// A turn that ends with no replay and no output answers the pending
// prompt, as a local slash command does.
func TestRead_turnWithoutReplayIsOurs(t *testing.T) {
	synthetic := `{"type":"assistant","message":{"model":"<synthetic>","content":[{"type":"text","text":"usage"}]}}`
	tests := []struct {
		name  string
		lines []string
	}{
		{"local command", []string{lineSystemInit, synthetic, lineResultOK}},
		{"result without init", []string{lineResultOK}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := startPeer(t, peerScript([]string{lineInitOK}, tt.lines))

			if _, err := c.Send("hi"); err != nil {
				t.Fatal(err)
			}
			readUntil(t, c, isAnchorFlush)
			if _, err := c.Send("next"); err != nil {
				t.Errorf("Send after the turn: %v", err)
			}
		})
	}
}

func TestSend_escapesLeadingSlash(t *testing.T) {
	obs := &recordingObserver{}
	c := startPeer(t, peerScript([]string{lineInitOK}), WithObserver(obs))

	if _, err := c.Send("/clear the cache"); err != nil {
		t.Fatal(err)
	}
	sent := obs.sentLines()
	var msg userMessage
	if err := json.Unmarshal([]byte(sent[len(sent)-1]), &msg); err != nil {
		t.Fatal(err)
	}
	if msg.Message.Content != `\/clear the cache` {
		t.Errorf("sent %q, want the slash escaped", msg.Message.Content)
	}
}

func TestRead_lineLongerThanReadBuffer(t *testing.T) {
	const n = 200_000
	longDelta := "printf '%s' '{\"type\":\"stream_event\",\"event\":{\"type\":\"content_block_delta\",\"index\":0," +
		"\"delta\":{\"type\":\"text_delta\",\"text\":\"'; head -c " + strconv.Itoa(n) + " /dev/zero | tr '\\0' a; printf '\"}}}\\n'; "
	script := "read -r _; printf '%s\\n' '" + lineInitOK + "'; read -r _; printf '%s\\n' '" + lineSystemInit + "' '" +
		replayLine("hi") + "' '" + lineMessageStart + "' '" + lineTextStart + "'; " + longDelta +
		"printf '%s\\n' '" + lineBlockStop + "' '" + lineResultOK + "'; cat >/dev/null"
	c := startPeer(t, script)

	if _, err := c.Send("hi"); err != nil {
		t.Fatal(err)
	}
	msgs := readUntil(t, c, isAnchorFlush)
	if out, _ := accumulate(t, msgs[:len(msgs)-1]).Content.(agent.Output); len(out.Text) != n {
		t.Errorf("output length = %d, want %d", len(out.Text), n)
	}
}

func TestInterrupt(t *testing.T) {
	obs := &recordingObserver{}
	c := startPeer(t, peerScript(
		[]string{lineInitOK},
		[]string{lineSystemInit},
		[]string{`{"type":"result","subtype":"error_during_execution","is_error":true,"terminal_reason":"aborted_streaming"}`},
	), WithObserver(obs))

	if err := c.Interrupt(); err != nil {
		t.Fatalf("Interrupt while idle: %v", err)
	}
	if n := len(obs.sentLines()); n != 1 {
		t.Fatalf("idle Interrupt wrote to the peer: %d lines sent", n)
	}
	if _, err := c.Send("count to 400"); err != nil {
		t.Fatal(err)
	}
	if err := c.Interrupt(); err != nil {
		t.Fatal(err)
	}
	msgs := readUntil(t, c, isAnchorFlush)
	if !strings.Contains(obs.sentLines()[2], `"subtype":"interrupt"`) {
		t.Errorf("interrupt request not sent: %v", obs.sentLines())
	}
	if len(msgs) != 2 || !isLog(msgs[0]) {
		t.Errorf("messages = %+v, want an interrupted log then the anchor flush", msgs)
	}
}

func TestStop_unblocksRead(t *testing.T) {
	c := startPeer(t, peerScript([]string{lineInitOK}))

	errc := make(chan error, 1)
	go func() {
		_, err := c.Read()
		errc <- err
	}()
	if err := c.Stop(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-errc:
		if err == nil {
			t.Fatal("expected Read error after Stop")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Read still blocked after Stop")
	}
}

func TestStop_escalates(t *testing.T) {
	tests := []struct {
		name   string
		script string
	}{
		// The peer ignores stdin EOF; Stop must fall back to signals.
		{"stdin ignored", "read -r _; printf '%s\\n' '" + lineInitOK + "'; exec sleep 60"},
		// A child inherits the pipes and outlives the peer; Stop must not
		// wait for it to close them.
		{"pipes held by a child", "read -r _; printf '%s\\n' '" + lineInitOK + "'; sleep 5 & exec sleep 60"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := startPeer(t, tt.script)
			c.stopGrace = 100 * time.Millisecond
			start := time.Now()
			if err := c.Stop(); err != nil {
				t.Fatal(err)
			}
			if elapsed := time.Since(start); elapsed > 2*time.Second {
				t.Errorf("Stop took %s", elapsed)
			}
		})
	}
}

func TestClaudeArgs(t *testing.T) {
	args := claudeArgs("haiku", "You are ada.")

	for _, want := range [][]string{
		{"--input-format", "stream-json"},
		{"--permission-prompt-tool", "stdio"},
		{"--thinking-display", "summarized"},
		{"--model", "haiku"},
		{"--append-system-prompt", "You are ada."},
		{"--disallowedTools", strings.Join(disallowedTools, ",")},
	} {
		i := slices.Index(args, want[0])
		if i < 0 || i+1 >= len(args) || args[i+1] != want[1] {
			t.Errorf("args missing %v: %v", want, args)
		}
	}
	if args := claudeArgs("", ""); slices.Contains(args, "--model") || slices.Contains(args, "--append-system-prompt") {
		t.Errorf("empty options produced flags: %v", args)
	}
}

func TestChildEnv(t *testing.T) {
	env := childEnv([]string{"HOME=/h", "CLAUDECODE=1", "CLAUDE_CODE_SESSION_ID=x", "CLAUDE_PID=9", "CLAUDE_EFFORT=high", "CLAUDE_CONFIG_DIR=/c"})

	want := append([]string{"HOME=/h", "CLAUDE_CONFIG_DIR=/c"}, addedEnv...)
	if !slices.Equal(env, want) {
		t.Errorf("env = %v, want %v", env, want)
	}
}
