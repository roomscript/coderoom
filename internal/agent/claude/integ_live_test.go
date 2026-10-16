//go:build integration

package claude_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/roomscript/coderoom/internal/agent"
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
// command and a file write. Read-only commands are auto-approved by Claude;
// the write asks for permission, which the adapter declines for now.
func TestClientLiveTurn(t *testing.T) {
	c, _, cwd := startLiveClient(t)
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

	sets := res.fileChanges()
	if len(sets) != 1 || sets[0].Status != agent.ToolStatusDeclined {
		t.Errorf("file changes = %+v, want one declined", sets)
	}
	if _, err := os.Stat(filepath.Join(cwd, "note.txt")); err == nil {
		t.Error("note.txt was written although the write was declined")
	}
	if len(res.logs) != 0 {
		t.Errorf("unexpected logs %q", res.logs)
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
