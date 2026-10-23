package codex

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

type process struct {
	cwd string
	cmd *exec.Cmd

	codexIn  io.WriteCloser
	codexOut *bufio.Reader
	codexErr io.ReadCloser
	stdout   io.Closer // the pipe codexOut reads, closed by abort

	askForApproval   AskForApprovalPolicy
	sandboxMode      SandboxMode
	model            string
	systemPrompt     string
	reasoningEffort  ReasoningEffort
	reasoningSummary ReasoningSummary
	appServerCmd     []string
}

func newProc(cwd string) *process {
	return &process{cwd: cwd, askForApproval: AskDefault, sandboxMode: SandboxDefault}
}

func (p *process) start() error {
	args := p.commandArgs()
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = p.cwd

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("codex stdin pipe: %w", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("codex stdout pipe: %w", err)
	}

	// Use os.Pipe instead of cmd.StderrPipe so that cmd.Wait does not add
	// the read end to closeAfterWait. StderrPipe closes the read end in Wait,
	// which makes io.ReadAll(codexErr) return nothing in the error path.
	// With a raw pipe we close the parent's write end after Start; the read
	// end stays open until the child exits and can be drained after Stop().
	stderrRead, stderrWrite, err := os.Pipe()
	if err != nil {
		return fmt.Errorf("codex stderr pipe: %w", err)
	}
	cmd.Stderr = stderrWrite

	if err := cmd.Start(); err != nil {
		_ = stderrRead.Close()
		_ = stderrWrite.Close()
		return fmt.Errorf("codex start: %w", err)
	}
	_ = stderrWrite.Close() // parent's copy no longer needed; child still has it

	p.codexIn = stdin
	p.codexOut = bufio.NewReader(stdout)
	p.codexErr = stderrRead
	p.stdout = stdout
	p.cmd = cmd
	return nil
}

// abort kills the process and closes our pipe ends, so blocked reads return
// even when a child that inherited the pipes outlives the kill. The stderr
// tail is lost.
func (p *process) abort() {
	_ = p.codexIn.Close()
	_ = p.cmd.Process.Kill()
	_ = p.stdout.Close()
	_ = p.codexErr.Close()
}

const stderrDrainTimeout = 2 * time.Second

// readStderrTail drains and closes stderr once the process has exited. A
// child that inherited the pipe can keep it open, so the read stops at a
// deadline and returns what arrived by then.
func (p *process) readStderrTail() string {
	if f, ok := p.codexErr.(*os.File); ok {
		_ = f.SetReadDeadline(time.Now().Add(stderrDrainTimeout))
	}
	b, _ := io.ReadAll(p.codexErr)
	_ = p.codexErr.Close()
	return strings.TrimRight(string(b), "\r\n")
}

func (p *process) commandArgs() []string {
	if len(p.appServerCmd) > 0 {
		return append([]string(nil), p.appServerCmd...)
	}
	return codexArgs(p.askForApproval, p.sandboxMode, p.reasoningEffort, p.reasoningSummary)
}

// codexPackage returns the npm package npx runs.
// CODEX_VERSION_OVERRIDE pins a specific npm version for integration testing.
func codexPackage() string {
	if v := os.Getenv("CODEX_VERSION_OVERRIDE"); v != "" {
		return "@openai/codex@" + v
	}
	return "@openai/codex"
}

// codexArgs returns the command and arguments for the Codex app-server.
func codexArgs(
	askForApproval AskForApprovalPolicy,
	sandboxMode SandboxMode,
	reasoningEffort ReasoningEffort,
	reasoningSummary ReasoningSummary,
) []string {
	args := []string{"npx", codexPackage()}
	if askForApproval != AskDefault {
		args = append(args, "--ask-for-approval", string(askForApproval))
	}
	if sandboxMode != SandboxDefault {
		args = append(args, "--sandbox", string(sandboxMode))
	}
	if reasoningEffort != ReasoningDefault {
		args = append(args, "-c", "model_reasoning_effort="+string(reasoningEffort))
	}
	if reasoningSummary != ReasoningSummaryDefault {
		args = append(
			args,
			"-c", "model_reasoning_summary="+string(reasoningSummary),
			"-c", "model_supports_reasoning_summaries=true",
		)
	}
	args = append(args, "app-server")
	return args
}
