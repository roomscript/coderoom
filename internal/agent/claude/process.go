package claude

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// baseArgs is the long-running stream-json invocation. See
// docs/playground/poc-claude-stream-json.md for the evidence behind each flag.
var baseArgs = []string{
	"--print", "--verbose",
	"--input-format", "stream-json",
	"--output-format", "stream-json",
	"--replay-user-messages",
	"--include-partial-messages",
	"--permission-prompts", "host",
	// Without it, permission prompts are denied without reaching the host.
	"--permission-prompt-tool", "stdio",
	// Without it, reasoning arrives as empty thinking deltas.
	"--thinking-display", "summarized",
}

// disallowedTools have no role in a room: they ask questions outside the
// approval flow, start turns on their own, or reach other local sessions.
var disallowedTools = []string{
	"AskUserQuestion",
	"CronCreate", "CronDelete", "CronList", "ScheduleWakeup", "Monitor",
	"ListAgents", "SendMessage", "PushNotification", "RemoteTrigger",
}

// strippedEnvPrefixes are variables a parent Claude Code session exports. A
// child that inherits them may behave as a nested session.
var strippedEnvPrefixes = []string{"CLAUDECODE=", "CLAUDE_CODE_", "CLAUDE_PID=", "CLAUDE_EFFORT="}

// addedEnv prevents turns the agent would start on its own.
var addedEnv = []string{
	"CLAUDE_CODE_DISABLE_BACKGROUND_TASKS=1",
	"CLAUDE_CODE_DISABLE_CRON=1",
}

type process struct {
	cwd          string
	sessionID    string // passed with --session-id; the ID --resume accepts
	model        string
	systemPrompt string
	command      []string // overrides the claude invocation, for tests

	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	stderr io.ReadCloser
}

func newProc(cwd string) *process {
	return &process{cwd: cwd}
}

func (p *process) start() error {
	args := p.commandArgs()
	cmd := exec.Command(args[0], args[1:]...) // #nosec G204 -- fixed binary, args built by this package
	cmd.Dir = p.cwd
	cmd.Env = childEnv(os.Environ())

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("claude stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("claude stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("claude stderr pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("claude start: %w", err)
	}
	p.cmd, p.stdin, p.stdout, p.stderr = cmd, stdin, stdout, stderr
	return nil
}

func (p *process) commandArgs() []string {
	if len(p.command) > 0 {
		return append([]string(nil), p.command...)
	}
	return claudeArgs(p.sessionID, p.model, p.systemPrompt)
}

// claudeArgs returns the command and arguments for a participant process.
func claudeArgs(sessionID, model, systemPrompt string) []string {
	args := append([]string{"claude"}, baseArgs...)
	args = append(args, "--session-id", sessionID)
	args = append(args, "--disallowedTools", strings.Join(disallowedTools, ","))
	if model != "" {
		args = append(args, "--model", model)
	}
	if systemPrompt != "" {
		args = append(args, "--append-system-prompt", systemPrompt)
	}
	return args
}

// childEnv returns parent without nested-session variables, plus addedEnv.
func childEnv(parent []string) []string {
	env := make([]string, 0, len(parent)+len(addedEnv))
	for _, kv := range parent {
		if !hasAnyPrefix(kv, strippedEnvPrefixes) {
			env = append(env, kv)
		}
	}
	return append(env, addedEnv...)
}

func hasAnyPrefix(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}
