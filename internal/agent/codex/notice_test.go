package codex

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/roomscript/coderoom/internal/agent"
)

// setupNoticeClient creates a client in noticePending state, simulating
// that SendNotice has been called and the turn is in flight.
func setupNoticeClient(t *testing.T, stdout string) *Client {
	t.Helper()
	c := newWithIO(t, nopWriteCloser{io.Discard}, bytes.NewBufferString(stdout), nil)
	c.turn.mu.Lock()
	c.turn.threadID = "t1"
	c.turn.state = turnState{kind: turnInflightUnknownID}
	c.turn.mu.Unlock()
	c.notice.mu.Lock()
	c.notice.state = noticePending
	c.notice.mu.Unlock()
	return c
}

func setupKeepaliveClient(t *testing.T, stdout string) *Client {
	t.Helper()
	return setupKeepaliveClientWithIO(t, nopWriteCloser{io.Discard}, bytes.NewBufferString(stdout))
}

func setupKeepaliveClientWithIO(t *testing.T, stdin io.WriteCloser, stdout io.Reader) *Client {
	t.Helper()
	c := &Client{proc: newProc("test")}
	c.proc.codexIn = stdin
	c.proc.codexOut = bufio.NewReader(stdout)
	c.proc.codexErr = io.NopCloser(bytes.NewBuffer(nil))
	c.rpc.obs = noopObserver{}
	c.initRead()
	c.initApprovals()
	c.lifecycle.ctx, c.lifecycle.cancelFn = context.WithCancel(context.Background()) // #nosec: G118
	t.Cleanup(c.lifecycle.cancelFn)
	c.turn.threadID = "t1"
	c.turn.state = turnState{kind: turnInflightUnknownID}
	c.notice.mu.Lock()
	c.notice.state = noticePending
	c.notice.kind = noticeTurnKeepalive
	c.notice.mu.Unlock()
	c.initWorkers()
	return c
}

const turnStarted = `{"method":"turn/started","params":{"threadId":"t1","turn":{"id":"u1"}}}` + "\n"
const turnCompleted = `{"method":"turn/completed","params":{}}` + "\n"
const turnFailed = `{"method":"turn/failed","params":{}}` + "\n"

func agentDelta(text string) string {
	return `{"method":"item/agentMessage/delta","params":{"delta":"` + text + `"}}` + "\n"
}

func TestKeepaliveFilter_nonCompliantOutputIsSuppressed(t *testing.T) {
	c := setupKeepaliveClient(t, turnStarted+agentDelta(`unexpected prose`)+turnCompleted)

	msg, err := c.Read()
	if err != nil {
		t.Fatalf("read keepalive completion: %v", err)
	}
	if _, ok := msg.Content.(agent.KeepAlive); !ok {
		t.Fatalf("completion content = %T, want agent.KeepAlive", msg.Content)
	}
}

func TestKeepaliveFilter_suppressesToolNotifications(t *testing.T) {
	tests := []struct {
		name     string
		itemType string
		wire     string
	}{
		{
			name:     "command execution",
			itemType: "commandExecution",
			wire: `{"method":"item/started","params":{"turnId":"u1","item":{"type":"commandExecution","id":"cmd1","command":"pwd","cwd":"/tmp","status":"inProgress"}}}` + "\n" +
				`{"method":"item/commandExecution/outputDelta","params":{"turnId":"u1","itemId":"cmd1","delta":"output"}}` + "\n",
		},
		{
			name:     "file change",
			itemType: "fileChange",
			wire: `{"method":"item/started","params":{"turnId":"u1","item":{"type":"fileChange","id":"patch1","status":"inProgress","changes":[]}}}` + "\n" +
				`{"method":"item/fileChange/patchUpdated","params":{"turnId":"u1","itemId":"patch1","changes":[]}}` + "\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := setupKeepaliveClient(t, turnStarted+tt.wire+turnCompleted)
			msg, err := c.Read()
			if err != nil {
				t.Fatalf("read tool diagnostic: %v", err)
			}
			log, ok := msg.Content.(agent.Log)
			if !ok {
				t.Fatalf("first content = %#v, want interruption diagnostic", msg.Content)
			}
			for _, want := range []string{"Keepalive interrupted", tt.itemType, "item/started", "normal participant work is unaffected"} {
				if !strings.Contains(log.Text, want) {
					t.Errorf("diagnostic %q does not contain %q", log.Text, want)
				}
			}
			if strings.Contains(log.Text, "SECURITY") {
				t.Errorf("diagnostic retained alarming SECURITY label: %q", log.Text)
			}
			msg, err = c.Read()
			if err != nil {
				t.Fatalf("read keepalive completion: %v", err)
			}
			if _, ok := msg.Content.(agent.KeepAlive); !ok {
				t.Fatalf("completion content = %T, want agent.KeepAlive", msg.Content)
			}
		})
	}
}

func TestKeepaliveFilter_explainsUnrecognizedActivity(t *testing.T) {
	tests := []struct {
		name string
		wire string
		want string
	}{
		{
			name: "unknown item type",
			wire: `{"method":"item/started","params":{"turnId":"u1","item":{"type":"futureItem","id":"item1"}}}` + "\n",
			want: "unrecognized item type futureItem",
		},
		{
			name: "malformed lifecycle payload",
			wire: `{"method":"item/started","params":[]}` + "\n",
			want: "malformed item lifecycle payload",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := setupKeepaliveClient(t, turnStarted+tt.wire+turnCompleted)
			msg, err := c.Read()
			if err != nil {
				t.Fatalf("read interruption diagnostic: %v", err)
			}
			log, ok := msg.Content.(agent.Log)
			if !ok {
				t.Fatalf("first content = %T, want agent.Log", msg.Content)
			}
			for _, want := range []string{tt.want, "item/started", "please report this event with the Codex version"} {
				if !strings.Contains(log.Text, want) {
					t.Errorf("diagnostic %q does not contain %q", log.Text, want)
				}
			}
		})
	}
}

func TestKeepaliveFilter_allowsUserMessageLifecycle(t *testing.T) {
	tests := []struct {
		name   string
		method string
	}{
		{name: "started", method: "item/started"},
		{name: "completed", method: "item/completed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stdin := &bytes.Buffer{}
			lifecycle := `{"method":"` + tt.method + `","params":{"turnId":"u1","item":{"type":"userMessage","id":"msg1","content":[]}}}` + "\n"
			c := setupKeepaliveClientWithIO(t, nopWriteCloser{stdin}, bytes.NewBufferString(turnStarted+lifecycle+turnCompleted))

			msg, err := c.Read()
			if err != nil {
				t.Fatalf("read keepalive completion: %v", err)
			}
			if _, ok := msg.Content.(agent.KeepAlive); !ok {
				t.Fatalf("completion content = %T, want agent.KeepAlive", msg.Content)
			}
			if strings.Contains(stdin.String(), `"method":"turn/interrupt"`) {
				t.Fatalf("user message lifecycle triggered interrupt: %s", stdin.String())
			}
		})
	}
}

func TestKeepaliveFilter_autoDeclinesApproval(t *testing.T) {
	stdin := &bytes.Buffer{}
	wire := turnStarted +
		`{"id":41,"method":"item/commandExecution/requestApproval","params":{"command":"pwd","cwd":"/tmp"}}` + "\n" +
		turnCompleted
	c := setupKeepaliveClientWithIO(t, nopWriteCloser{stdin}, bytes.NewBufferString(wire))

	msg, err := c.Read()
	if err != nil {
		t.Fatalf("read approval diagnostic: %v", err)
	}
	if _, ok := msg.Content.(agent.Log); !ok {
		t.Fatalf("first content = %T, want agent.Log", msg.Content)
	}
	log := msg.Content.(agent.Log)
	for _, want := range []string{"Keepalive interrupted", "approval request", "requestApproval", "auto-declined", "normal participant work is unaffected"} {
		if !strings.Contains(log.Text, want) {
			t.Errorf("diagnostic %q does not contain %q", log.Text, want)
		}
	}
	msg, err = c.Read()
	if err != nil {
		t.Fatalf("read keepalive completion: %v", err)
	}
	if _, ok := msg.Content.(agent.KeepAlive); !ok {
		t.Fatalf("completion content = %T, want agent.KeepAlive", msg.Content)
	}
	if got := stdin.String(); !strings.Contains(got, `"id":41`) || !strings.Contains(got, `"decision":"decline"`) {
		t.Fatalf("approval response = %q, want decline for request 41", got)
	}
}

func TestKeepaliveFilter_failedTurnStillCompletes(t *testing.T) {
	c := setupKeepaliveClient(t, turnStarted+turnFailed)

	msg, err := c.Read()
	if err != nil {
		t.Fatalf("read keepalive completion: %v", err)
	}
	if _, ok := msg.Content.(agent.KeepAlive); !ok {
		t.Fatalf("completion content = %T, want agent.KeepAlive", msg.Content)
	}
}

// TestNoticeFilter_compliantAck verifies that a response of {"acknowledge":true}
// is silently discarded, but still produces a turn-level Output+ModeFlush so
// downstream consumers can treat SendNotice as a complete turn lifecycle.
func TestNoticeFilter_compliantAck(t *testing.T) {
	stdout := turnStarted +
		agentDelta(`{\"acknowledge\":true}`) +
		turnCompleted
	c := setupNoticeClient(t, stdout)

	msg, err := c.Read()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Mode != agent.ModeFlush {
		t.Fatalf("expected ModeFlush, got %v", msg.Mode)
	}
	if _, ok := msg.Content.(agent.Output); !ok {
		t.Fatalf("expected Output content, got %T", msg.Content)
	}
}

// TestNoticeFilter_compliantAckWithExtraFields verifies that extra JSON fields
// alongside "acknowledge":true are intentionally ignored — still discarded, but
// still produces a turn-level flush.
func TestNoticeFilter_compliantAckWithExtraFields(t *testing.T) {
	stdout := turnStarted +
		agentDelta(`{\"acknowledge\":true,\"notes\":\"logged\"}`) +
		turnCompleted
	c := setupNoticeClient(t, stdout)

	msg, err := c.Read()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Mode != agent.ModeFlush {
		t.Fatalf("expected ModeFlush, got %v", msg.Mode)
	}
	if _, ok := msg.Content.(agent.Output); !ok {
		t.Fatalf("expected Output content, got %T", msg.Content)
	}
}

// TestNoticeFilter_emptyResponse treats a response with no deltas as an ack and
// still emits a turn-level flush.
func TestNoticeFilter_emptyResponse(t *testing.T) {
	stdout := turnStarted + turnCompleted
	c := setupNoticeClient(t, stdout)

	msg, err := c.Read()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Mode != agent.ModeFlush {
		t.Fatalf("expected ModeFlush, got %v", msg.Mode)
	}
	if _, ok := msg.Content.(agent.Output); !ok {
		t.Fatalf("expected Output content, got %T", msg.Content)
	}
}

// TestNoticeFilter_nonCompliantProse verifies that a prose response (first char
// is not '{') is relayed as Reasoning then a reasoning-stream flush then a
// turn-level flush.
func TestNoticeFilter_nonCompliantProse(t *testing.T) {
	stdout := turnStarted +
		agentDelta(`I think the code looks fine.`) +
		turnCompleted
	c := setupNoticeClient(t, stdout)

	msg, err := c.Read()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	r, ok := msg.Content.(agent.Reasoning)
	if !ok || r.Text != "I think the code looks fine." {
		t.Errorf("expected Reasoning relay, got mode=%v content=%T", msg.Mode, msg.Content)
	}

	// reasoning-stream flush
	msg, err = c.Read()
	if err != nil {
		t.Fatalf("unexpected error on reasoning flush: %v", err)
	}
	if msg.Mode != agent.ModeFlush {
		t.Errorf("expected ModeFlush for reasoning stream end, got mode=%v", msg.Mode)
	}

	// turn-level flush
	msg, err = c.Read()
	if err != nil {
		t.Fatalf("unexpected error on turn flush: %v", err)
	}
	if msg.Mode != agent.ModeFlush {
		t.Errorf("expected ModeFlush for turn end, got mode=%v", msg.Mode)
	}
}

// TestNoticeFilter_nonCompliantJSON verifies that a JSON response that parses
// but lacks "acknowledge":true is replayed as Reasoning then reasoning-stream
// flush then turn-level flush.
func TestNoticeFilter_nonCompliantJSON(t *testing.T) {
	stdout := turnStarted +
		agentDelta(`{\"status\":\"ok\"}`) +
		turnCompleted
	c := setupNoticeClient(t, stdout)

	msg, err := c.Read()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := msg.Content.(agent.Reasoning); !ok {
		t.Errorf("expected Reasoning for non-ack JSON, got mode=%v content=%T", msg.Mode, msg.Content)
	}

	// reasoning-stream flush
	msg, err = c.Read()
	if err != nil {
		t.Fatalf("unexpected error on reasoning flush: %v", err)
	}
	if msg.Mode != agent.ModeFlush {
		t.Errorf("expected ModeFlush for reasoning stream end, got mode=%v", msg.Mode)
	}

	// turn-level flush
	msg, err = c.Read()
	if err != nil {
		t.Fatalf("unexpected error on turn flush: %v", err)
	}
	if msg.Mode != agent.ModeFlush {
		t.Errorf("expected ModeFlush for turn end, got mode=%v", msg.Mode)
	}
}

// TestNoticeFilter_nonCompliantMultiDeltaRelay verifies that multiple deltas
// are all relayed as reasoning when the first char is not '{'.
//
//nolint:cyclop
func TestNoticeFilter_nonCompliantMultiDeltaRelay(t *testing.T) {
	stdout := turnStarted +
		agentDelta(`step one`) +
		agentDelta(` step two`) +
		turnCompleted
	c := setupNoticeClient(t, stdout)

	msg, err := c.Read()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	r, ok := msg.Content.(agent.Reasoning)
	if !ok || r.Text != "step one" {
		t.Errorf("expected first Reasoning delta, got mode=%v content=%T", msg.Mode, msg.Content)
	}

	msg, err = c.Read()
	if err != nil {
		t.Fatalf("unexpected error on second delta: %v", err)
	}
	r, ok = msg.Content.(agent.Reasoning)
	if !ok || r.Text != " step two" {
		t.Errorf("expected second Reasoning delta, got mode=%v content=%T", msg.Mode, msg.Content)
	}

	// reasoning-stream flush
	msg, err = c.Read()
	if err != nil {
		t.Fatalf("unexpected error on reasoning flush: %v", err)
	}
	if msg.Mode != agent.ModeFlush {
		t.Errorf("expected ModeFlush for reasoning stream end, got mode=%v", msg.Mode)
	}

	// turn-level flush
	msg, err = c.Read()
	if err != nil {
		t.Fatalf("unexpected error on turn flush: %v", err)
	}
	if msg.Mode != agent.ModeFlush {
		t.Errorf("expected ModeFlush for turn end, got mode=%v", msg.Mode)
	}
}

// TestNoticeFilter_turnFailed_buffering verifies that a failed notice turn
// while buffering is silently discarded, but still emits a turn-level flush so
// downstream consumers can treat SendNotice as a complete lifecycle.
func TestNoticeFilter_turnFailed_buffering(t *testing.T) {
	stdout := turnStarted +
		agentDelta(`{\"partial`) +
		turnFailed
	c := setupNoticeClient(t, stdout)

	msg, err := c.Read()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Mode != agent.ModeFlush {
		t.Fatalf("expected ModeFlush, got %v", msg.Mode)
	}
	if _, ok := msg.Content.(agent.Output); !ok {
		t.Fatalf("expected Output content, got %T", msg.Content)
	}
}

// TestNoticeFilter_turnFailed_relaying verifies that a failed notice turn while
// relaying emits a reasoning-stream flush then a turn-level flush.
func TestNoticeFilter_turnFailed_relaying(t *testing.T) {
	stdout := turnStarted +
		agentDelta(`some prose`) +
		turnFailed
	c := setupNoticeClient(t, stdout)

	msg, err := c.Read()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := msg.Content.(agent.Reasoning); !ok {
		t.Errorf("expected Reasoning before failed turn, got mode=%v content=%T", msg.Mode, msg.Content)
	}

	// reasoning-stream flush
	msg, err = c.Read()
	if err != nil {
		t.Fatalf("unexpected error on reasoning flush: %v", err)
	}
	if msg.Mode != agent.ModeFlush {
		t.Errorf("expected ModeFlush for reasoning stream end, got mode=%v", msg.Mode)
	}

	// turn-level flush
	msg, err = c.Read()
	if err != nil {
		t.Fatalf("unexpected error on turn flush: %v", err)
	}
	if msg.Mode != agent.ModeFlush {
		t.Errorf("expected ModeFlush after failed relaying notice, got mode=%v", msg.Mode)
	}
}

// TestNoticeFilter_noNoticeState verifies that normal (non-notice) turns are
// unaffected when no notice is active.
func TestNoticeFilter_noNoticeState(t *testing.T) {
	stdout := `{"method":"item/agentMessage/delta","params":{"itemId":"msg1","turnId":"turn1","delta":"hello"}}` + "\n" +
		`{"method":"turn/completed","params":{"threadId":"th1","turn":{"id":"turn1","status":"completed","items":[{"type":"agentMessage","id":"msg1"}]}}}` + "\n"
	c := newWithIO(t, nopWriteCloser{io.Discard}, bytes.NewBufferString(stdout), nil)

	msg, err := c.Read()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out, ok := msg.Content.(agent.Output)
	if !ok || out.Text != "hello" {
		t.Errorf("expected Output{hello}, got mode=%v content=%T", msg.Mode, msg.Content)
	}
	msg, err = c.Read()
	if err != nil {
		t.Fatalf("unexpected error on turn flush: %v", err)
	}
	if msg.Mode != agent.ModeFlush {
		t.Errorf("expected ModeFlush for turn/completed, got mode=%v", msg.Mode)
	}
}
