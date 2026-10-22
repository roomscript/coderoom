package claude

import (
	"context"
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

const (
	lineInitOK     = `{"type":"control_response","response":{"subtype":"success","request_id":"coderoom-init","response":{}}}`
	lineSettingsOK = `{"type":"control_response","response":{"subtype":"success","request_id":"coderoom-settings","response":{"applied":{"model":"claude-haiku-4-5","effort":null}}}}`

	// peerNext reads the next stdin line, answering the startup
	// get_settings request on the way. It fails at EOF.
	peerNext = `next() { while read -r l; do case $l in *'"coderoom-settings"'*) printf '%s\n' '` + lineSettingsOK + `';; *) return 0;; esac; done; return 1; }; `
)

// peerScript returns a shell script for a fake claude process. Each step
// reads one line from stdin, then prints its lines. The startup get_settings
// request is answered between steps and does not count as a step. After the
// last step the peer drains stdin, so it exits when the client closes stdin.
// A test that must act before the peer goes on starts a new step and then
// writes a line, usually with Interrupt.
func peerScript(steps ...[]string) string {
	var sb strings.Builder
	sb.WriteString(peerNext)
	for _, step := range steps {
		sb.WriteString("next; ")
		for _, l := range step {
			sb.WriteString("printf '%s\\n' '" + strings.ReplaceAll(l, "'", "'\"'\"'") + "'; ")
		}
	}
	sb.WriteString("while next; do :; done")
	return sb.String()
}

// startPeer starts a Client against a fake peer and stops it at test end.
func startPeer(t *testing.T, script string, opts ...Option) *Client {
	t.Helper()
	opts = append([]Option{WithCommand("sh", "-c", script)}, opts...)
	c := New(t.TempDir(), opts...)
	var n int
	c.newPromptID = func() string { n++; return promptID(n) }
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
	want := []struct{ id, subtype string }{
		{initRequestID, controlInitialize},
		{settingsRequestID, controlGetSettings},
	}
	if len(sent) != len(want) {
		t.Fatalf("sent %d lines, want initialize and get_settings", len(sent))
	}
	for i, w := range want {
		req := parseSentControl(t, sent[i])
		if req.Type != "control_request" || req.RequestID != w.id || req.Request.Subtype != w.subtype {
			t.Errorf("request %d = %+v, want %s %s", i, req, w.id, w.subtype)
		}
	}
}

// sentControl is a control request the client wrote.
type sentControl struct {
	Type      string `json:"type"`
	RequestID string `json:"request_id"`
	Request   struct {
		Subtype  string         `json:"subtype"`
		Model    string         `json:"model"`
		Settings map[string]any `json:"settings"`
	} `json:"request"`
}

func parseSentControl(t *testing.T, line string) sentControl {
	t.Helper()
	var req sentControl
	if err := json.Unmarshal([]byte(line), &req); err != nil {
		t.Fatalf("parse request %s: %v", line, err)
	}
	return req
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

// sendSignal closes sent on the first line written to the peer.
type sendSignal struct {
	once sync.Once
	sent chan struct{}
}

func (o *sendSignal) OnSend(string)    { o.once.Do(func() { close(o.sent) }) }
func (o *sendSignal) OnReceive(string) {}

func TestStart_parentContextCancelledDuringHandshake(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	obs := &sendSignal{sent: make(chan struct{})}
	// The peer never answers initialize.
	c := New(t.TempDir(), WithCommand("sh", "-c", "cat >/dev/null"), WithContext(ctx), WithObserver(obs))

	errc := make(chan error, 1)
	go func() { errc <- c.Start() }()
	waitClosed(t, obs.sent, "initialize not sent")
	cancel()
	select {
	case err := <-errc:
		if err == nil {
			t.Fatal("expected Start to fail")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Start still waiting after the parent context ended")
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
	c := startPeer(t, peerNext+"next; echo 'warning: something' >&2; printf '%s\\n' '"+lineInitOK+"'; while next; do :; done")

	msgs := readUntil(t, c, isLog)
	if log, _ := msgs[len(msgs)-1].Content.(agent.Log); log.Text != "warning: something" {
		t.Errorf("log = %q", log.Text)
	}
}

func TestRead_selfStartedTurnSummarisedAsLog(t *testing.T) {
	// After the handshake the peer starts a turn on its own (as a finished
	// background task or a cron prompt would), then waits for the test prompt.
	script := peerNext + "next; printf '%s\\n' '" + lineInitOK + "' '" + lineSystemInit + "' '" + lineMessageStart + "' '" +
		lineTextStart + "' '" + textDeltaLine("tick") + "' '" + lineBlockStop + "' '" + lineResultOK + "'; while next; do :; done"
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
	tests := []struct {
		name   string
		prompt string
		replay string
	}{
		{"reworded", "hi", "hi, reworded"},
		{"pasted local command markup", "<local-command-stdout>pasted</local-command-stdout>", "<local-command-stdout>pasted</local-command-stdout>"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := startPeer(t, peerScript(
				[]string{lineInitOK},
				[]string{lineSystemInit, replayLine(tt.replay), lineMessageStart, lineTextStart, textDeltaLine("ok"), lineBlockStop, lineResultOK},
			))

			if _, err := c.Send(tt.prompt); err != nil {
				t.Fatal(err)
			}
			msgs := readUntil(t, c, isAnchorFlush)
			if out, _ := accumulate(t, msgs[:len(msgs)-1]).Content.(agent.Output); out.Text != "ok" {
				t.Errorf("reply = %q, want ok", out.Text)
			}
			if _, err := c.Send("next"); err != nil {
				t.Errorf("Send after the reply: %v", err)
			}
		})
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

func TestRead_replayNotOursSummarisedOnce(t *testing.T) {
	// A replay arrives in a self-started turn while no prompt is pending.
	// Then the test's own turn marks the end.
	c := startPeer(t, peerScript(
		[]string{lineInitOK, lineSystemInit, lineMessageStart, lineTextStart, textDeltaLine("tick"), lineBlockStop,
			replayLineID("not ours", "someone-else"), lineResultOK},
		[]string{lineSystemInit, replayLine("hi"), lineResultOK},
	))
	readUntil(t, c, isLog)
	if _, err := c.Send("hi"); err != nil {
		t.Fatal(err)
	}

	msgs := readUntil(t, c, isAnchorFlush)
	if slices.ContainsFunc(msgs, isLog) {
		t.Errorf("messages = %+v, want the self-started turn summarised once", msgs)
	}
}

func TestRead_replayTakesOverWithOpenCommand(t *testing.T) {
	// A command from the self-started part is still running when the replay
	// arrives; its result must not surface in our turn.
	c := startPeer(t, peerScript(
		[]string{lineInitOK},
		[]string{
			lineSystemInit, lineMessageStart, toolUseLine("toolu_1", "Bash", `{"command":"ls"}`),
			replayLine("hi"),
			toolResultLine("toolu_1", `"a.txt"`, false, `{"stdout":"a.txt","stderr":""}`),
			lineMessageStart, lineTextStart, textDeltaLine("ok"), lineBlockStop, lineResultOK,
		},
	))

	if _, err := c.Send("hi"); err != nil {
		t.Fatal(err)
	}
	msgs := readUntil(t, c, isAnchorFlush)
	for _, m := range msgs {
		if m.StreamID == "claude:command:toolu_1" {
			t.Errorf("self-started command delivered in our turn: %+v", m)
		}
	}
	if out, _ := accumulate(t, msgs[1:len(msgs)-1]).Content.(agent.Output); out.Text != "ok" {
		t.Errorf("reply = %q, want ok", out.Text)
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
	script := peerNext + "next; printf '%s\\n' '" + lineInitOK + "'; next; printf '%s\\n' '" + lineSystemInit + "' '" +
		replayLine("hi") + "' '" + lineMessageStart + "' '" + lineTextStart + "'; " + longDelta +
		"printf '%s\\n' '" + lineBlockStop + "' '" + lineResultOK + "'; while next; do :; done"
	c := startPeer(t, script)

	if _, err := c.Send("hi"); err != nil {
		t.Fatal(err)
	}
	msgs := readUntil(t, c, isAnchorFlush)
	if out, _ := accumulate(t, msgs[:len(msgs)-1]).Content.(agent.Output); len(out.Text) != n {
		t.Errorf("output length = %d, want %d", len(out.Text), n)
	}
}

func TestRead_failedTurnExplanation(t *testing.T) {
	tests := []struct {
		name   string
		result string
		want   string
	}{
		{
			name:   "usage limit with success subtype",
			result: `{"type":"result","subtype":"success","is_error":true,"terminal_reason":"api_error","api_error_status":429,"api_error":"usage_limit_reached","result":"You've hit your session limit · resets 4:30pm (Europe/Berlin)"}`,
			want:   "claude: You've hit your session limit · resets 4:30pm (Europe/Berlin)",
		},
		{
			name:   "diagnostic details with blank result",
			result: `{"type":"result","subtype":"error_during_execution","is_error":true,"result":"  ","errors":[" ","[ede_diagnostic] request failed","Try again later"]}`,
			want:   "claude: turn failed: [ede_diagnostic] request failed; Try again later",
		},
		{
			name:   "result and error details",
			result: `{"type":"result","is_error":true,"result":"Request rejected","errors":["Check your account"]}`,
			want:   "claude: Request rejected; Check your account",
		},
		{
			name:   "fallback reason without success",
			result: `{"type":"result","subtype":"success","is_error":true,"terminal_reason":"api_error","result":" ","errors":[""]}`,
			want:   "claude: turn failed (api_error)",
		},
		{
			name:   "fallback subtype and reason",
			result: `{"type":"result","subtype":"error_during_execution","is_error":true,"terminal_reason":"api_error"}`,
			want:   "claude: turn failed (error_during_execution, api_error)",
		},
		{
			name:   "fallback without explanation",
			result: `{"type":"result","subtype":"success","is_error":true}`,
			want:   "claude: turn failed",
		},
		{
			name:   "interrupted tools",
			result: `{"type":"result","is_error":true,"terminal_reason":"aborted_tools","result":"Ignored explanation"}`,
			want:   "claude: turn interrupted",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checkFailedTurn(t, tt.result, tt.want)
		})
	}
}

func checkFailedTurn(t *testing.T, result, want string) {
	t.Helper()
	c := startPeer(t, peerScript([]string{lineInitOK}, []string{result}))
	if _, err := c.Send("hi"); err != nil {
		t.Fatal(err)
	}
	msgs := readUntil(t, c, isAnchorFlush)
	if len(msgs) != 2 || msgs[0].Content != (agent.Log{Text: want}) {
		t.Errorf("messages = %+v, want log %q then anchor flush", msgs, want)
	}
	if _, err := c.Send("next turn"); err != nil {
		t.Fatalf("Send after failed turn: %v", err)
	}
}

func TestInterrupt(t *testing.T) {
	obs := &recordingObserver{}
	c := startPeer(t, peerScript(
		[]string{lineInitOK},
		[]string{lineSystemInit},
		[]string{`{"type":"result","subtype":"error_during_execution","is_error":true,"terminal_reason":"aborted_streaming"}`},
	), WithObserver(obs))

	before := len(obs.sentLines())
	if err := c.Interrupt(); err != nil {
		t.Fatalf("Interrupt while idle: %v", err)
	}
	if n := len(obs.sentLines()) - before; n != 0 {
		t.Fatalf("idle Interrupt wrote to the peer: %d lines sent", n)
	}
	if _, err := c.Send("count to 400"); err != nil {
		t.Fatal(err)
	}
	if err := c.Interrupt(); err != nil {
		t.Fatal(err)
	}
	msgs := readUntil(t, c, isAnchorFlush)
	if !strings.Contains(obs.sentLines()[before+1], `"subtype":"interrupt"`) {
		t.Errorf("interrupt request not sent: %v", obs.sentLines())
	}
	// A pending prompt that has seen no turn end must not be released.
	if len(msgs) != 2 || msgs[0].Content != (agent.Log{Text: "claude: turn interrupted"}) {
		t.Errorf("messages = %+v, want an interrupted log then the anchor flush", msgs)
	}
}

func TestInterrupt_releasesPromptWithoutTurn(t *testing.T) {
	// A self-started turn ends while the prompt is pending, and no turn
	// answers it: only Interrupt can free the client.
	c := startPeer(t, peerScript(
		[]string{lineInitOK},
		[]string{lineSystemInit, lineMessageStart, lineTextStart, textDeltaLine("tick"), lineBlockStop, lineResultOK},
	))
	if _, err := c.Send("hi"); err != nil {
		t.Fatal(err)
	}
	readUntil(t, c, isLog) // the self-started turn's summary

	if err := c.Interrupt(); err != nil {
		t.Fatal(err)
	}
	msgs := readUntil(t, c, isAnchorFlush)
	if len(msgs) != 2 || !isLog(msgs[0]) {
		t.Errorf("messages = %+v, want a log then the anchor flush", msgs)
	}
	if _, err := c.Send("next"); err != nil {
		t.Errorf("Send after Interrupt: %v", err)
	}
}

func TestRead_lateReplayOfReleasedPrompt(t *testing.T) {
	// Interrupt releases "old"; Claude answers it after "new" was sent.
	// The old turn must not complete the new prompt's turn.
	oldFailed := `{"type":"result","subtype":"error_during_execution","is_error":true,"terminal_reason":"aborted_streaming"}`
	tests := []struct {
		name    string
		oldTurn []string // after the old prompt's replay
	}{
		{"old answer streams", []string{lineMessageStart, lineTextStart, textDeltaLine("old answer"), lineBlockStop, lineResultOK}},
		{"old turn ends without output", []string{oldFailed}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			late := append([]string{lineSystemInit, replayLineID("old", promptID(1))}, tt.oldTurn...)
			late = append(late, lineSystemInit, replayLineID("new", promptID(2)), lineMessageStart, lineTextStart,
				textDeltaLine("new answer"), lineBlockStop, lineResultOK)
			c := startPeer(t, peerScript(
				[]string{lineInitOK},
				[]string{lineSystemInit, lineMessageStart, lineTextStart, textDeltaLine("tick"), lineBlockStop, lineResultOK},
				[]string{}, // the interrupt
				late,
			))
			if _, err := c.Send("old"); err != nil {
				t.Fatal(err)
			}
			readUntil(t, c, isLog) // the self-started turn's summary
			if err := c.Interrupt(); err != nil {
				t.Fatal(err)
			}
			readUntil(t, c, isAnchorFlush) // the release
			if _, err := c.Send("new"); err != nil {
				t.Fatal(err)
			}

			msgs := readUntil(t, c, isAnchorFlush)
			var reply []agent.Message
			for _, m := range msgs[:len(msgs)-1] {
				if !isLog(m) {
					reply = append(reply, m)
				}
			}
			if out, _ := accumulate(t, reply).Content.(agent.Output); out.Text != "new answer" {
				t.Errorf("reply = %q, want the new answer (messages %+v)", out.Text, msgs)
			}
		})
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
		{"stdin ignored", peerNext + "next; printf '%s\\n' '" + lineInitOK + "'; next; exec sleep 60"},
		// A child inherits the pipes and outlives the peer; Stop must not
		// wait for it to close them.
		{"pipes held by a child", peerNext + "next; printf '%s\\n' '" + lineInitOK + "'; sleep 5 & next; exec sleep 60"},
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
