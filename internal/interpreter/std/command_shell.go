package std

import (
	"errors"
	"fmt"
	"strings"

	"github.com/roomscript/coderoom/internal/agent"
	"github.com/roomscript/coderoom/internal/interpreter/runtime"
	"github.com/roomscript/coderoom/internal/promptlang"
	"github.com/roomscript/coderoom/internal/room"
	"github.com/roomscript/coderoom/internal/shell"
)

// ShellCommand is stateless; Prepare captures arguments for each invocation.
type ShellCommand struct{}

var _ runtime.Command = ShellCommand{}

// Name identifies the command in the catalog.
func (ShellCommand) Name() string { return "shell" }

// Usage supplies the command's help example.
func (ShellCommand) Usage() string { return "/shell <program>" }

// Description supplies the command's help description.
func (ShellCommand) Description() string { return "execute a shell program" }

// Statement identifies the accepted parsed statement.
func (ShellCommand) Statement() promptlang.Statement { return promptlang.Shell{} }

// Prepare captures shell input and execution capabilities for one use.
func (ShellCommand) Prepare(parsed promptlang.ParsedStatement, ctx runtime.Context) (runtime.Invocation, error) {
	statement, ok := parsed.Value.(promptlang.Shell)
	if !ok {
		return nil, errors.New("shell requires a shell statement")
	}
	if ctx.Shell == nil {
		return nil, errors.New("programmes require shell execution")
	}
	return &shellInvocation{launcher: ctx.Shell, command: statement.Program.Value, displayCommand: statement.Program.Value, cwd: ctx.Shell.Cwd()}, nil
}

type shellInvocation struct {
	launcher       runtime.ShellLauncher
	command        string
	displayCommand string
	cwd            string
}

func (i *shellInvocation) Go(complete func(runtime.Completion)) error {
	err := i.launcher.Go(i.command, func(result shell.Result) {
		record := room.NewAgentRecord("shell", agent.Message{
			Mode:    agent.ModeSingle,
			Content: agent.Command{Command: i.displayCommand, Cwd: i.cwd, Output: FormatShellResult(result), ExitCode: result.ExitCode},
		})
		complete(runtime.Completion{Records: []room.Record{record}, Err: result.Err})
	})
	if err != nil {
		return fmt.Errorf("launch shell: %w", err)
	}
	return nil
}

// FormatShellResult preserves the shell transcript's status and output sections.
func FormatShellResult(result shell.Result) string {
	sections := []string{"status: " + string(result.Status)}
	if result.Stdout != "" {
		sections = append(sections, "stdout:\n"+result.Stdout)
	}
	if result.Stderr != "" {
		sections = append(sections, "stderr:\n"+result.Stderr)
	}
	if result.Err != nil {
		sections = append(sections, "error:\n"+result.Err.Error())
	}
	return strings.Join(sections, "\n")
}
