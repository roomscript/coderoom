package claude

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/roomscript/coderoom/internal/agent"
	"github.com/roomscript/coderoom/internal/approval"
)

func TestApprovalEvidencePreservesFullInput(t *testing.T) {
	raw := []byte(`{"tool_name":"Bash","input":{"command":"echo ` + strings.Repeat("x", 300) + `"},"unknownSafetyField":{"expansion":"host"}}`)
	var tool canUseTool
	if err := json.Unmarshal(raw, &tool); err != nil {
		t.Fatal(err)
	}
	req := approvalRequestFor(tool)
	commandOperation, ok := req.Details.Operation.(agent.CommandOperation)
	if !ok {
		t.Fatal("expected command operation")
	}
	if string(req.Details.Raw) != string(raw) || len(commandOperation.Command) <= 160 {
		t.Fatal("full action evidence was shortened or lost")
	}
	if req.Presentation.Summary != "" {
		t.Fatal("command summary must be derived by room")
	}
}

func TestApprovalOperationNormalization(t *testing.T) {
	for _, tt := range []struct {
		tool, input string
		want        agent.Operation
	}{
		{"Read", `{"file_path":"/project/file"}`, agent.FileOperation{Paths: []string{"/project/file"}, AllTargetsKnown: true}},
		{"Write", `{"file_path":"/project/file","content":"data"}`, agent.FileOperation{Paths: []string{"/project/file"}, Write: true, AllTargetsKnown: true, ProposedChanges: []agent.FileChange{{Path: "/project/file", ChangeKind: "write", Diff: "data"}}}},
		{"Edit", `{"file_path":"/project/file"}`, agent.FileOperation{Paths: []string{"/project/file"}, Write: true, AllTargetsKnown: true}},
		{"Write", `{"file_path":"/project/file","content":123}`, agent.FileOperation{Paths: []string{"/project/file"}, Write: true, AllTargetsKnown: true}},
		{"WebFetch", `{"url":"https://example.com"}`, agent.HTTPOperation{Method: "GET", URL: "https://example.com", FollowsRedirects: true}},
		{"Read", `{"file_path":"relative"}`, nil},
		{"WebSearch", `{}`, nil},
	} {
		t.Run(tt.tool+tt.input, func(t *testing.T) {
			req := approvalRequestFor(canUseTool{ToolName: tt.tool, Input: json.RawMessage(tt.input)})
			if !reflect.DeepEqual(req.Details.Operation, tt.want) {
				t.Fatalf("operation = %+v; want %+v", req.Details.Operation, tt.want)
			}
			if req.Details.Backend != "claude" || req.Details.Tool != tt.tool || len(req.Details.Raw) == 0 {
				t.Fatal("approval metadata lost")
			}
		})
	}
}

func TestProposedFileApprovalEvidence(t *testing.T) {
	for _, tt := range []struct {
		tool, input, kind, diff string
	}{
		{"Write", `{"file_path":"/project/file","content":"first\nsecond\n"}`, "write", "first\nsecond\n"},
		{"Write", `{"file_path":"/project/file","content":""}`, "write", ""},
		{"Edit", `{"file_path":"/project/file","old_string":"old\ntext","new_string":"new\ntext"}`, "update", "@@ replace matching occurrence @@\n-old\n-text\n+new\n+text\n"},
		{"Edit", `{"file_path":"/project/file","old_string":"old","new_string":"","replace_all":true}`, "update", "@@ replace all matching occurrences @@\n-old\n"},
	} {
		t.Run(tt.tool+tt.input, func(t *testing.T) {
			req := approvalRequestFor(canUseTool{ToolName: tt.tool, Input: json.RawMessage(tt.input)})
			op := req.Details.Operation.(agent.FileOperation)
			want := []agent.FileChange{{Path: "/project/file", ChangeKind: tt.kind, Diff: tt.diff}}
			if !reflect.DeepEqual(op.ProposedChanges, want) {
				t.Fatalf("changes = %+v, want %+v", op.ProposedChanges, want)
			}
			p := approval.Policy{ProjectRoot: "/project"}
			got := p.Evaluate(context.Background(), op)
			op.ProposedChanges = nil
			if got != p.Evaluate(context.Background(), op) {
				t.Fatal("proposed content changed policy outcome")
			}
		})
	}
}

func TestUnsupportedApprovalDisplayEvidence(t *testing.T) {
	plan := "# Plan\n" + strings.Repeat("Complete step\n", 100) + "last step\x1b[2J"
	planInput, err := json.Marshal(map[string]any{"plan": plan, "allowedPrompts": []string{"specific permission"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct{ tool, input, want string }{
		{"ExitPlanMode", string(planInput), plan},
		{"NotebookEdit", `{"notebook_path":"/project/n.ipynb","new_source":"complete cell source"}`, "complete cell source"},
		{"WebFetch", `{"url":"https://example.com","prompt":"` + strings.Repeat("full prompt ", 100) + `tail"}`, strings.Repeat("full prompt ", 100) + "tail"},
		{"UnknownTool", `{"nested":{"scope":"` + strings.Repeat("scope ", 100) + `tail"}}`, strings.Repeat("scope ", 100) + "tail"},
	} {
		t.Run(tt.tool, func(t *testing.T) {
			req := approvalRequestFor(canUseTool{ToolName: tt.tool, Input: json.RawMessage(tt.input)})
			if !strings.Contains(req.Presentation.Evidence, tt.want) {
				t.Fatalf("display evidence lost: %s", req.Presentation.Evidence)
			}
			if tt.tool == "ExitPlanMode" && !strings.Contains(req.Presentation.Evidence, "specific permission") {
				t.Fatal("additional plan permissions lost")
			}
			p := approval.Policy{}
			before := p.Evaluate(context.Background(), req.Details.Operation)
			req.Presentation = agent.ApprovalPresentation{Summary: "different", Explanation: "different", Evidence: "different"}
			if after := p.Evaluate(context.Background(), req.Details.Operation); after != before {
				t.Fatal("display changed policy")
			}
		})
	}
}

// fakeListener answers with a fixed decision and records the requests.
type fakeListener struct {
	mu     sync.Mutex
	reqs   []agent.ApprovalRequest
	choice agent.ApprovalOption
	err    error
}

func (l *fakeListener) Decide(_ context.Context, req agent.ApprovalRequest) (agent.ApprovalDecision, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.reqs = append(l.reqs, req)
	return agent.ApprovalDecision{Choice: l.choice}, l.err
}

func (l *fakeListener) requests() []agent.ApprovalRequest {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.reqs)
}

// blockingListener blocks until its context is cancelled.
type blockingListener struct{ called chan struct{} }

func (l blockingListener) Decide(ctx context.Context, _ agent.ApprovalRequest) (agent.ApprovalDecision, error) {
	close(l.called)
	<-ctx.Done()
	return agent.ApprovalDecision{}, fmt.Errorf("decide: %w", ctx.Err())
}

const lineSuggestions = `[{"type":"addRules","rules":[{"toolName":"Bash","ruleContent":"touch m.txt"}],"behavior":"allow","destination":"localSettings"},{"type":"setMode","mode":"acceptEdits","destination":"session"}]`

func canUseToolLine(tool, input, suggestions string) string {
	return canUseToolLineID("perm-1", tool, input, suggestions)
}

func canUseToolLineID(requestID, tool, input, suggestions string) string {
	return `{"type":"control_request","request_id":"` + requestID + `","request":{"subtype":"can_use_tool","tool_name":"` + tool +
		`","input":` + input + `,"permission_suggestions":` + suggestions + `,"tool_use_id":"toolu_1"}}`
}

// sentPermission is the part of a can_use_tool response the tests check.
type sentPermission struct {
	RequestID string
	Behavior  string
	Message   string
	Interrupt bool
	Input     json.RawMessage
	Perms     []map[string]any
}

// runPermission drives one turn with a permission request and returns the
// response the client wrote and the turn's messages.
func runPermission(t *testing.T, request string, opts ...Option) (sentPermission, []agent.Message) {
	t.Helper()
	obs := &recordingObserver{}
	opts = append(opts, WithObserver(obs))
	c := startPeer(t, peerScript(
		[]string{lineInitOK},
		[]string{lineSystemInit, replayLine("go"), request},
		[]string{lineResultOK},
	), opts...)
	if _, err := c.Send("go"); err != nil {
		t.Fatal(err)
	}
	msgs := readUntil(t, c, isAnchorFlush)
	sent := obs.sentLines()
	return parseSentPermission(t, sent[len(sent)-1]), msgs
}

func parseSentPermission(t *testing.T, line string) sentPermission {
	t.Helper()
	var resp struct {
		Response struct {
			RequestID string `json:"request_id"`
			Response  struct {
				Behavior           string           `json:"behavior"`
				Message            string           `json:"message"`
				Interrupt          bool             `json:"interrupt"`
				UpdatedInput       json.RawMessage  `json:"updatedInput"`
				UpdatedPermissions []map[string]any `json:"updatedPermissions"`
			} `json:"response"`
		} `json:"response"`
	}
	if err := json.Unmarshal([]byte(line), &resp); err != nil {
		t.Fatalf("parse response %s: %v", line, err)
	}
	r := resp.Response.Response
	return sentPermission{
		RequestID: resp.Response.RequestID, Behavior: r.Behavior, Message: r.Message,
		Interrupt: r.Interrupt, Input: r.UpdatedInput, Perms: r.UpdatedPermissions,
	}
}

type permissionChoiceTest struct {
	name          string
	choice        agent.ApprovalOption
	suggestions   string
	wantBehavior  string
	wantInterrupt bool
	wantPerms     int
	wantLog       bool
}

func TestPermission_choices(t *testing.T) {
	tests := []permissionChoiceTest{
		{"accept", agent.OptionAccept, lineSuggestions, "allow", false, 0, false},
		{"accept for session", agent.OptionAcceptForSession, lineSuggestions, "allow", false, 2, false},
		{"accept for session without suggestions", agent.OptionAcceptForSession, `[]`, "allow", false, 0, true},
		{"decline", agent.OptionDecline, lineSuggestions, "deny", false, 0, false},
		{"cancel", agent.OptionCancel, lineSuggestions, "deny", true, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := `{"command":"touch m.txt"}`
			sent, msgs := runPermission(t, canUseToolLine("Bash", input, tt.suggestions),
				WithApprovalListener(&fakeListener{choice: tt.choice}))
			checkPermissionChoice(t, tt, input, sent, msgs)
		})
	}
}

func checkPermissionChoice(t *testing.T, tt permissionChoiceTest, input string, sent sentPermission, msgs []agent.Message) {
	t.Helper()
	if sent.RequestID != "perm-1" || sent.Behavior != tt.wantBehavior || sent.Interrupt != tt.wantInterrupt {
		t.Errorf("response = %+v", sent)
	}
	if tt.wantBehavior == "allow" && string(sent.Input) != input {
		t.Errorf("updatedInput = %s, want the original input", sent.Input)
	}
	if tt.wantBehavior == "deny" && sent.Message != declineMessage {
		t.Errorf("message = %q", sent.Message)
	}
	checkSessionPermissions(t, sent.Perms, tt.wantPerms)
	if hasLog := slices.ContainsFunc(msgs, isLog); hasLog != tt.wantLog {
		t.Errorf("log emitted = %v, want %v: %+v", hasLog, tt.wantLog, msgs)
	}
}

func checkSessionPermissions(t *testing.T, perms []map[string]any, want int) {
	t.Helper()
	if len(perms) != want {
		t.Errorf("updatedPermissions = %v, want %d", perms, want)
	}
	for _, p := range perms {
		if p["destination"] != "session" {
			t.Errorf("permission %v not scoped to the session", p)
		}
	}
}

func TestPermission_declinedWithoutValidDecision(t *testing.T) {
	tests := []struct {
		name string
		opts []Option
	}{
		{"no listener", nil},
		{"listener error", []Option{WithApprovalListener(&fakeListener{err: errors.New("ui gone")})}},
		{"choice not offered", []Option{WithApprovalListener(&fakeListener{choice: agent.OptionAcceptForSession})}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// WebFetch is offered accept and decline only.
			sent, msgs := runPermission(t, canUseToolLine("WebFetch", `{"url":"https://example.com"}`, `[]`), tt.opts...)
			if sent.Behavior != "deny" {
				t.Errorf("behavior = %q, want deny", sent.Behavior)
			}
			if !slices.ContainsFunc(msgs, isLog) {
				t.Errorf("no log for the auto-decline: %+v", msgs)
			}
		})
	}
}

func TestPermission_selfStartedTurnDenied(t *testing.T) {
	webFetchStart := `{"type":"stream_event","event":{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_1","name":"WebFetch"}}}`
	tests := []struct {
		name      string
		sendFirst bool // a prompt is pending when the turn starts
		turn      []string
	}{
		{"mapped tool", false, []string{lineSystemInit, toolUseLine("toolu_1", "Bash", `{"command":"ls"}`),
			canUseToolLine("Bash", `{"command":"ls"}`, `[]`)}},
		// The decoder maps nothing for WebFetch; the stream event alone shows
		// the turn is not ours.
		{"unmapped tool", false, []string{lineSystemInit, lineMessageStart, webFetchStart,
			toolUseLine("toolu_1", "WebFetch", `{"url":"https://example.com"}`),
			canUseToolLine("WebFetch", `{"url":"https://example.com"}`, `[]`)}},
		{"unmapped tool before the replay", true, []string{lineSystemInit, lineMessageStart, webFetchStart,
			toolUseLine("toolu_1", "WebFetch", `{"url":"https://example.com"}`),
			canUseToolLine("WebFetch", `{"url":"https://example.com"}`, `[]`)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			obs := &recordingObserver{}
			l := &fakeListener{choice: agent.OptionAccept}
			// Claude starts a turn on its own and asks for a tool; the turn
			// ends once the request is answered.
			steps := [][]string{append([]string{lineInitOK}, tt.turn...), {lineResultOK}}
			if tt.sendFirst {
				steps = [][]string{{lineInitOK}, tt.turn, {lineResultOK}}
			}
			c := startPeer(t, peerScript(steps...), WithApprovalListener(l), WithObserver(obs))
			if tt.sendFirst {
				if _, err := c.Send("hi"); err != nil {
					t.Fatal(err)
				}
			}

			msgs := readUntil(t, c, func(m agent.Message) bool {
				log, ok := m.Content.(agent.Log)
				return ok && strings.Contains(log.Text, "on its own:")
			})
			if !slices.ContainsFunc(msgs[:len(msgs)-1], isLog) {
				t.Errorf("messages = %+v, want a log for the denial before the turn summary", msgs)
			}
			// The startup get_settings request may follow the answer.
			sent := slices.DeleteFunc(obs.sentLines(), func(l string) bool {
				return !strings.Contains(l, `"type":"control_response"`)
			})
			if answer := parseSentPermission(t, sent[len(sent)-1]); answer.Behavior != "deny" || answer.Message != selfTurnMessage {
				t.Errorf("response = %+v, want deny for a self-started turn", answer)
			}
			if n := len(l.requests()); n != 0 {
				t.Errorf("listener called %d times, want 0", n)
			}
		})
	}
}

func TestPermission_askUserQuestionNotForwarded(t *testing.T) {
	l := &fakeListener{choice: agent.OptionAccept}
	sent, _ := runPermission(t, canUseToolLine("AskUserQuestion", `{"questions":[]}`, `[]`), WithApprovalListener(l))

	if sent.Behavior != "deny" || sent.Message != askInReplyMessage {
		t.Errorf("response = %+v, want deny asking for a plain-text question", sent)
	}
	if n := len(l.requests()); n != 0 {
		t.Errorf("listener called %d times, want 0", n)
	}
}

func TestPermission_pendingDecisionDoesNotBlockRead(t *testing.T) {
	l := blockingListener{called: make(chan struct{})}
	script := peerNext + "next; printf '%s\\n' '" + lineInitOK + "'; next; printf '%s\\n' '" + lineSystemInit + "' '" + replayLine("go") + "' '" +
		canUseToolLine("Bash", `{"command":"ls"}`, `[]`) + "'; echo 'still here' >&2; while next; do :; done"
	c := startPeer(t, script, WithApprovalListener(l))
	if _, err := c.Send("go"); err != nil {
		t.Fatal(err)
	}

	readUntil(t, c, isLog) // the stderr line arrives while the decision is pending
	select {
	case <-l.called:
	case <-time.After(5 * time.Second):
		t.Fatal("listener not called")
	}
	done := make(chan struct{})
	go func() {
		_ = c.Stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Stop blocked on a pending approval")
	}
}

func TestPermission_parentContextCancelledStopsProcess(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	l := newFirstBlocksListener()
	c := startPeer(t, peerScript(
		[]string{lineInitOK},
		[]string{lineSystemInit, replayLine("go"), canUseToolLine("Bash", `{"command":"ls"}`, `[]`)},
	), WithApprovalListener(l), WithContext(ctx))
	if _, err := c.Send("go"); err != nil {
		t.Fatal(err)
	}
	waitClosed(t, l.asked, "listener not asked")

	cancel()
	waitClosed(t, l.withdrawn, "listener still waiting after the parent context ended")
	readUntilExit(t, c)
}

func TestPermission_processExitMidTurn(t *testing.T) {
	l := newFirstBlocksListener()
	script := peerNext + "next; printf '%s\\n' '" + lineInitOK + "'; next; printf '%s\\n' '" + lineSystemInit + "' '" +
		replayLine("go") + "' '" + canUseToolLine("Bash", `{"command":"ls"}`, `[]`) + "'; next; exit 1"
	c := startPeer(t, script, WithApprovalListener(l))
	if _, err := c.Send("go"); err != nil {
		t.Fatal(err)
	}
	waitClosed(t, l.asked, "listener not asked")

	// The peer exits once the test writes another line.
	if err := c.Interrupt(); err != nil {
		t.Fatal(err)
	}
	waitClosed(t, l.withdrawn, "listener still waiting after the process exited")
	readUntilExit(t, c)
}

func TestPermission_declinedFileChangeEndToEnd(t *testing.T) {
	write := toolUseLine("toolu_1", "Write", `{"file_path":"/w/a.txt","content":"a"}`)
	c := startPeer(t, peerScript(
		[]string{lineInitOK},
		[]string{lineSystemInit, replayLine("go"), write, canUseToolLine("Write", `{"file_path":"/w/a.txt","content":"a"}`, `[]`)},
		[]string{toolResultLine("toolu_1", `"`+declineMessage+`"`, true, `"Error: `+declineMessage+`"`), lineResultOK},
	), WithApprovalListener(&fakeListener{choice: agent.OptionDecline}))

	if _, err := c.Send("go"); err != nil {
		t.Fatal(err)
	}
	msgs := readUntil(t, c, isAnchorFlush)
	var change []agent.Message
	for _, m := range msgs {
		if m.StreamID == "claude:filechange:toolu_1" {
			change = append(change, m)
		}
	}
	if set, _ := accumulate(t, change).Content.(agent.FileChangeSet); set.Status != agent.ToolStatusDeclined {
		t.Errorf("status = %q, want declined", set.Status)
	}
}

// firstBlocksListener blocks on its first request until the request is
// withdrawn, then accepts every later one. asked is closed when the first
// request arrives.
type firstBlocksListener struct {
	mu        sync.Mutex
	calls     int
	asked     chan struct{}
	withdrawn chan struct{}
}

func newFirstBlocksListener() *firstBlocksListener {
	return &firstBlocksListener{asked: make(chan struct{}), withdrawn: make(chan struct{})}
}

func (l *firstBlocksListener) Decide(ctx context.Context, _ agent.ApprovalRequest) (agent.ApprovalDecision, error) {
	l.mu.Lock()
	l.calls++
	first := l.calls == 1
	l.mu.Unlock()
	if !first {
		return agent.ApprovalDecision{Choice: agent.OptionAccept}, nil
	}
	close(l.asked)
	<-ctx.Done()
	close(l.withdrawn)
	return agent.ApprovalDecision{}, fmt.Errorf("decide: %w", ctx.Err())
}

func TestPermission_pendingRequestEndsWithItsTurn(t *testing.T) {
	obs := &recordingObserver{}
	l := newFirstBlocksListener()
	c := startPeer(t, peerScript(
		[]string{lineInitOK},
		[]string{lineSystemInit, replayLine("one"), canUseToolLineID("perm-1", "Bash", `{"command":"ls"}`, `[]`)},
		// The interrupt ends the turn while perm-1 is still pending.
		[]string{lineResultOK},
		[]string{lineSystemInit, replayLineID("two", promptID(2)), canUseToolLineID("perm-2", "Bash", `{"command":"ls"}`, `[]`)},
		[]string{lineResultOK},
	), WithApprovalListener(l), WithObserver(obs))

	if _, err := c.Send("one"); err != nil {
		t.Fatal(err)
	}
	waitClosed(t, l.asked, "listener not asked for perm-1")
	if err := c.Interrupt(); err != nil {
		t.Fatal(err)
	}
	readUntil(t, c, isAnchorFlush)
	waitClosed(t, l.withdrawn, "pending request not withdrawn at the end of its turn")
	if _, err := c.Send("two"); err != nil {
		t.Fatal(err)
	}
	readUntil(t, c, isAnchorFlush)

	sent := obs.sentLines()
	if answer := parseSentPermission(t, sent[len(sent)-1]); answer.RequestID != "perm-2" || answer.Behavior != "allow" {
		t.Errorf("last answer = %+v, want allow for perm-2", answer)
	}
	for _, line := range sent {
		if strings.Contains(line, `"perm-1"`) {
			t.Errorf("answered the withdrawn request: %s", line)
		}
	}
}

func TestPermission_withdrawnByClaude(t *testing.T) {
	l := newFirstBlocksListener()
	// Claude withdraws perm-1 while the listener waits; the turn goes on.
	c := startPeer(t, peerScript(
		[]string{lineInitOK},
		[]string{lineSystemInit, replayLine("go"), canUseToolLine("Bash", `{"command":"ls"}`, `[]`)},
		// Sent once the test interrupts, after the listener has the request.
		[]string{cancelLine("perm-1")},
	), WithApprovalListener(l))

	if _, err := c.Send("go"); err != nil {
		t.Fatal(err)
	}
	waitClosed(t, l.asked, "listener not asked for perm-1")
	if err := c.Interrupt(); err != nil {
		t.Fatal(err)
	}
	waitClosed(t, l.withdrawn, "listener still waiting after control_cancel_request")
}

func TestPermission_withdrawnWhileQueuedIsNotAsked(t *testing.T) {
	obs := &recordingObserver{}
	l := newFirstBlocksListener()
	// perm-2 waits behind perm-1 and is withdrawn before its turn comes.
	// perm-3 follows; the worker handles requests in order, so its answer
	// means perm-2 has been dealt with.
	c := startPeer(t, peerScript(
		[]string{lineInitOK},
		[]string{lineSystemInit, replayLine("go"), canUseToolLineID("perm-1", "Bash", `{"command":"ls"}`, `[]`)},
		// Sent once the test interrupts, after the listener has perm-1.
		[]string{
			canUseToolLineID("perm-2", "Bash", `{"command":"pwd"}`, `[]`), cancelLine("perm-2"), cancelLine("perm-1"),
			canUseToolLineID("perm-3", "Bash", `{"command":"id"}`, `[]`),
		},
		// Sent after the answer to perm-3.
		[]string{lineResultOK},
	), WithApprovalListener(l), WithObserver(obs))

	if _, err := c.Send("go"); err != nil {
		t.Fatal(err)
	}
	waitClosed(t, l.asked, "listener not asked for perm-1")
	if err := c.Interrupt(); err != nil {
		t.Fatal(err)
	}
	readUntil(t, c, isAnchorFlush)
	l.mu.Lock()
	calls := l.calls
	l.mu.Unlock()
	if calls != 2 {
		t.Errorf("listener asked %d times, want 2 (perm-1 and perm-3)", calls)
	}
	for _, line := range obs.sentLines() {
		if strings.Contains(line, `"perm-1"`) || strings.Contains(line, `"perm-2"`) {
			t.Errorf("answered a withdrawn request: %s", line)
		}
	}
}

func cancelLine(requestID string) string {
	return `{"type":"control_cancel_request","request_id":"` + requestID + `"}`
}

func TestApprovalRequestFor(t *testing.T) {
	tests := []struct {
		tool        string
		input       string
		wantKind    agent.ApprovalKind
		wantAsk     string
		wantOptions []agent.ApprovalOption
	}{
		{"Bash", `{"command":"go test ./...","description":"Run tests"}`, agent.ApprovalCommandExecution,
			"", allOptions},
		{"Write", `{"file_path":"/w/a.go","content":"x"}`, agent.ApprovalFileChange, "", allOptions},
		{"NotebookEdit", `{"notebook_path":"/w/n.ipynb"}`, agent.ApprovalFileChange, "", allOptions},
		{"ExitPlanMode", `{"plan":"# Plan: add tests\n\n## Steps\n1. Write them"}`, agent.ApprovalPermissions,
			"Plan approval", acceptDecline},
		{"WebFetch", `{"url":"https://example.com"}`, agent.ApprovalPermissions,
			"Tool: WebFetch", acceptDecline},
		{"Bash", `{}`, agent.ApprovalCommandExecution, "", allOptions},
	}
	for _, tt := range tests {
		t.Run(tt.tool+" "+tt.input, func(t *testing.T) {
			got := approvalRequestFor(canUseTool{ToolName: tt.tool, Input: json.RawMessage(tt.input)})
			if got.Kind != tt.wantKind || got.Presentation.Summary != tt.wantAsk || !slices.Equal(got.Options, tt.wantOptions) {
				t.Errorf("got %+v, want kind %s, ask %q, options %v", got, tt.wantKind, tt.wantAsk, tt.wantOptions)
			}
		})
	}
}

func TestBashApprovalRetainsExplanation(t *testing.T) {
	explanation := strings.Repeat("supplied explanation ", 100)
	input, err := json.Marshal(map[string]string{"command": "/opt/bin/git diff", "description": explanation})
	if err != nil {
		t.Fatal(err)
	}
	req := approvalRequestFor(canUseTool{ToolName: "Bash", Input: input})
	op := req.Details.Operation.(agent.CommandOperation)
	if req.Presentation.Explanation != explanation || op.Command != "/opt/bin/git diff" || op.Cwd != "" {
		t.Fatalf("lost evidence: %+v", op)
	}
}

func TestPermissionPresentation(t *testing.T) {
	for _, tt := range []struct{ tool, input, summary, evidence string }{
		{"Read", `{"file_path":"/project/file","offset":42,"future":"extra"}`, "Tool: Read", `"future": "extra"`},
		{"WebFetch", `{"url":"https://example.com","prompt":"full prompt"}`, "Tool: WebFetch", "full prompt"},
		{"Unknown", `{"nested":{"future":"extra"}}`, "Tool: Unknown", `"future": "extra"`},
		{"ExitPlanMode", `{"plan":"first\nlast","future":"extra"}`, "Plan approval", "Plan:\nfirst\nlast\nAdditional input:"},
		{"Unknown", `broken`, "Tool: Unknown", "Input (unparsed):\nbroken"},
		{"Read", `null`, "Tool: Read", "Tool input unavailable."},
		{"Read", ``, "Tool: Read", "Tool input unavailable."},
	} {
		t.Run(tt.tool+tt.input, func(t *testing.T) {
			req := approvalRequestFor(canUseTool{ToolName: tt.tool, Input: json.RawMessage(tt.input)})
			if req.Kind != agent.ApprovalPermissions || req.Presentation.Summary != tt.summary || req.Presentation.Explanation != "" || !strings.Contains(req.Presentation.Evidence, tt.evidence) {
				t.Fatalf("lost presentation: %+v", req)
			}
			if agent.ApprovalPrompt(req) != tt.summary {
				t.Fatal("permission prompt was independently composed")
			}
		})
	}
}

func TestCommonOperationSupplementalEvidence(t *testing.T) {
	for _, tt := range []struct{ tool, input, want string }{
		{"Bash", `{"command":"git diff","description":"supplied reason","future":"complete unknown"}`, "complete unknown"},
		{"Write", `{"file_path":"/file","content":"full contents","future":"complete unknown"}`, "complete unknown"},
		{"Edit", `{"file_path":"/file","old_string":"old","new_string":"new","replace_all":true,"future":"complete unknown"}`, "complete unknown"},
		{"Write", `{"file_path":"/file","content":123}`, `"content": 123`},
	} {
		t.Run(tt.tool+tt.input, func(t *testing.T) {
			req := approvalRequestFor(canUseTool{ToolName: tt.tool, Input: json.RawMessage(tt.input)})
			if req.Presentation.Summary != "" || !strings.Contains(req.Presentation.Evidence, tt.want) {
				t.Fatalf("lost supplemental input: %+v", req.Presentation)
			}
			if strings.Contains(req.Presentation.Evidence, "supplied reason") {
				t.Fatal("extracted explanation duplicated in evidence")
			}
		})
	}
}
