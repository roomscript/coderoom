//go:build integration

package claude_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/roomscript/coderoom/internal/agent"
	"github.com/roomscript/coderoom/internal/agent/claude"
)

// These tests run a real Claude Code. They check what fake peers cannot:
// that the flags and environment the adapter passes have the intended effect,
// and that the decoder handles the shapes the installed version emits.

// TestClientLiveToolSurface checks that --disallowedTools (comma-joined) and
// CLAUDE_CODE_DISABLE_CRON remove the tools a room participant must not use.
func TestClientLiveToolSurface(t *testing.T) {
	c, obs, _ := startLiveClient(t)
	readTurn(t, c, send(t, c, `Reply "hi" only.`))

	var init struct {
		Version string   `json:"claude_code_version"`
		Tools   []string `json:"tools"`
	}
	if err := json.Unmarshal(obs.initLine(), &init); err != nil {
		t.Fatalf("parse system/init: %v", err)
	}
	t.Logf("claude code %s", init.Version)
	if !slices.Contains(init.Tools, "Bash") {
		t.Errorf("Bash missing from tools %v", init.Tools)
	}
	removed := []string{
		"AskUserQuestion", "CronCreate", "CronDelete", "CronList", "ScheduleWakeup",
		"Monitor", "ListAgents", "SendMessage", "PushNotification", "RemoteTrigger",
	}
	for _, tool := range removed {
		if slices.Contains(init.Tools, tool) {
			t.Errorf("%s still available", tool)
		}
	}
}

// TestClientLiveTurn runs one turn with a successful command, a failing
// command and an approved file write. Read-only commands are auto-approved by
// Claude; only the write reaches the listener.
func TestClientLiveTurn(t *testing.T) {
	l := &fixedListener{choice: agent.OptionAccept}
	c, _, cwd := startLiveClient(t, claude.WithApprovalListener(l))
	prompt := "Do these steps in order, one tool call each, without retrying:\n" +
		"1. Run the Bash command `echo coderoom-live`.\n" +
		"2. Run the Bash command `ls missing-file`.\n" +
		"3. Create the file note.txt containing `hello` with the Write tool.\n" +
		"Then reply \"done\"."
	res := readTurn(t, c, send(t, c, prompt))

	checkCommand(t, res, "echo coderoom-live", func(cmd agent.Command) bool {
		return strings.Contains(cmd.Output, "coderoom-live") && cmd.ExitCode != nil && *cmd.ExitCode == 0
	})
	checkCommand(t, res, "ls missing-file", func(cmd agent.Command) bool {
		return cmd.ExitCode != nil && *cmd.ExitCode != 0
	})
	checkSingleFileChange(t, res, agent.ToolStatusCompleted)
	b, err := os.ReadFile(filepath.Join(cwd, "note.txt")) // #nosec G304 -- test temp dir
	if err != nil || !strings.Contains(string(b), "hello") {
		t.Errorf("note.txt = %q, %v; want it written", b, err)
	}
	if reqs := l.requests(); len(reqs) != 1 || reqs[0].Kind != agent.ApprovalFileChange {
		t.Errorf("approval requests = %+v, want one file change", reqs)
	}
	if len(res.logs) != 0 {
		t.Errorf("unexpected logs %q", res.logs)
	}
}

// TestClientLiveRefusedWrite checks decline and cancel. Cancel denies with
// interrupt, which must end the turn: the model gets no chance to continue.
func TestClientLiveRefusedWrite(t *testing.T) {
	tests := []struct {
		choice  agent.ApprovalOption
		wantLog bool // the turn-interrupted log
	}{
		{agent.OptionDecline, false},
		{agent.OptionCancel, true},
	}
	for _, tt := range tests {
		t.Run(string(tt.choice), func(t *testing.T) {
			c, _, cwd := startLiveClient(t, claude.WithApprovalListener(&fixedListener{choice: tt.choice}))
			prompt := "Create the file note.txt containing `hello` with the Write tool. Do not retry. Then reply \"done\"."
			res := readTurn(t, c, send(t, c, prompt))

			checkSingleFileChange(t, res, agent.ToolStatusDeclined)
			if _, err := os.Stat(filepath.Join(cwd, "note.txt")); err == nil {
				t.Error("note.txt was written although the write was refused")
			}
			if hasLog := len(res.logs) > 0; hasLog != tt.wantLog {
				t.Errorf("logs = %q, want a log: %v", res.logs, tt.wantLog)
			}
		})
	}
}

// TestClientLiveAcceptForSession checks that a session approval covers the
// next write without asking again, and that the rule stays in the process:
// Claude suggests localSettings, which would write into the project.
func TestClientLiveAcceptForSession(t *testing.T) {
	l := &fixedListener{choice: agent.OptionAcceptForSession}
	c, _, cwd := startLiveClient(t, claude.WithApprovalListener(l))
	prompt := "With the Write tool, create a.txt containing `a`, then b.txt containing `b`, one tool call each. " +
		"Do not retry. Then reply \"done\"."
	readTurn(t, c, send(t, c, prompt))

	for _, name := range []string{"a.txt", "b.txt"} {
		if _, err := os.Stat(filepath.Join(cwd, name)); err != nil {
			t.Errorf("%s not written: %v", name, err)
		}
	}
	if n := len(l.requests()); n != 1 {
		t.Errorf("approval requests = %d, want 1 (the second write covered by the session rule)", n)
	}
	if _, err := os.Stat(filepath.Join(cwd, ".claude")); err == nil {
		t.Error("session approval wrote settings into the project (.claude exists)")
	}
}

// TestClientLiveInterrupt interrupts a turn once it produces output. The
// turn must end with a log line and the anchor flush, not a Read error.
func TestClientLiveInterrupt(t *testing.T) {
	c, _, _ := startLiveClient(t)
	anchor := send(t, c, "Count from 1 to 500, one number per line, with no other text.")

	first, err := c.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if first.StreamID == anchor {
		t.Fatal("turn ended before it could be interrupted")
	}
	if err := c.Interrupt(); err != nil {
		t.Fatalf("Interrupt: %v", err)
	}
	res := readTurn(t, c, anchor)
	if len(res.logs) == 0 {
		t.Error("interrupted turn produced no log")
	}

	// The client accepts the next turn.
	readTurn(t, c, send(t, c, `Reply "hi" only.`))
}

// TestClientLiveInterruptPendingApproval interrupts while the listener
// waits. Claude withdraws the request, so the listener must stop waiting, and
// the client must accept the next turn.
func TestClientLiveInterruptPendingApproval(t *testing.T) {
	l := &waitingListener{called: make(chan struct{}), released: make(chan struct{})}
	c, _, cwd := startLiveClient(t, claude.WithApprovalListener(l))
	anchor := send(t, c, "Create the file note.txt containing `hello` with the Write tool. Do not retry.")

	turn := make(chan turnResult, 1)
	go func() { turn <- readTurn(t, c, anchor) }()
	select {
	case <-l.called:
	case <-time.After(liveTurnTimeout):
		t.Fatal("no approval request")
	}
	if err := c.Interrupt(); err != nil {
		t.Fatalf("Interrupt: %v", err)
	}
	select {
	case <-l.released:
	case <-time.After(30 * time.Second):
		t.Fatal("listener still waiting after the interrupt")
	}
	res := <-turn
	if len(res.logs) == 0 {
		t.Error("interrupted turn produced no log")
	}
	if _, err := os.Stat(filepath.Join(cwd, "note.txt")); err == nil {
		t.Error("note.txt was written")
	}
	readTurn(t, c, send(t, c, `Reply "hi" only.`))
}

func send(t *testing.T, c interface {
	Send(string) (agent.StreamID, error)
}, prompt string,
) agent.StreamID {
	t.Helper()
	anchor, err := c.Send(prompt)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	return anchor
}

func checkSingleFileChange(t *testing.T, res turnResult, want agent.ToolStatus) {
	t.Helper()
	sets := res.fileChanges()
	if len(sets) != 1 || sets[0].Status != want {
		t.Errorf("file changes = %+v, want one %s", sets, want)
	}
}

func checkCommand(t *testing.T, res turnResult, command string, ok func(agent.Command) bool) {
	t.Helper()
	for _, cmd := range res.commands() {
		if cmd.Command != command {
			continue
		}
		if !ok(cmd) {
			t.Errorf("%q: output %q, exit code %v", command, cmd.Output, cmd.ExitCode)
		}
		return
	}
	t.Errorf("command %q not run; commands: %+v", command, res.commands())
}
