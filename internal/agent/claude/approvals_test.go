package claude

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/roomscript/coderoom/internal/agent"
)

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
	return `{"type":"control_request","request_id":"perm-1","request":{"subtype":"can_use_tool","tool_name":"` + tool +
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
		[]string{lineSystemInit, request},
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
	script := "read -r _; printf '%s\\n' '" + lineInitOK + "'; read -r _; printf '%s\\n' '" + lineSystemInit + "' '" +
		canUseToolLine("Bash", `{"command":"ls"}`, `[]`) + "'; echo 'still here' >&2; cat >/dev/null"
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

func TestApprovalRequestFor(t *testing.T) {
	tests := []struct {
		tool        string
		input       string
		wantKind    agent.ApprovalKind
		wantAsk     string
		wantOptions []agent.ApprovalOption
	}{
		{"Bash", `{"command":"go test ./...","description":"Run tests"}`, agent.ApprovalCommandExecution,
			"approve command execution: go test ./... (Run tests)", allOptions},
		{"Write", `{"file_path":"/w/a.go","content":"x"}`, agent.ApprovalFileChange, "approve file changes: /w/a.go", allOptions},
		{"NotebookEdit", `{"notebook_path":"/w/n.ipynb"}`, agent.ApprovalFileChange, "approve file changes: /w/n.ipynb", allOptions},
		{"ExitPlanMode", `{"plan":"# Plan: add tests\n\n## Steps\n1. Write them"}`, agent.ApprovalPermissions,
			"approve plan: Plan: add tests", acceptDecline},
		{"WebFetch", `{"url":"https://example.com"}`, agent.ApprovalPermissions,
			`approve tool WebFetch: {"url":"https://example.com"}`, acceptDecline},
		{"Bash", `{}`, agent.ApprovalCommandExecution, "approve command execution: <unknown>", allOptions},
	}
	for _, tt := range tests {
		t.Run(tt.tool+" "+tt.input, func(t *testing.T) {
			got := approvalRequestFor(canUseTool{ToolName: tt.tool, Input: json.RawMessage(tt.input)})
			if got.Kind != tt.wantKind || got.Ask != tt.wantAsk || !slices.Equal(got.Options, tt.wantOptions) {
				t.Errorf("got %+v, want kind %s, ask %q, options %v", got, tt.wantKind, tt.wantAsk, tt.wantOptions)
			}
		})
	}
}

func TestTrimMiddle(t *testing.T) {
	got := trimMiddle(strings.Repeat("a", 10)+strings.Repeat("b", 10), 9)
	if got != "aaaa…bbbb" {
		t.Errorf("trimMiddle = %q", got)
	}
}
