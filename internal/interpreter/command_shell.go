package interpreter

import (
	"context"
	"strings"

	"github.com/trigosec/coderoom/internal/promptlang"
	"github.com/trigosec/coderoom/internal/room"
	"github.com/trigosec/coderoom/internal/shell"
)

const shellRecordAlias = "shell"

// ShellRunner executes one shell program.
type ShellRunner interface {
	Run(context.Context, string, string) shell.Result
}

// ShellRunnerFunc adapts a function to ShellRunner.
type ShellRunnerFunc func(context.Context, string, string) shell.Result

// Run executes the adapted function.
func (f ShellRunnerFunc) Run(ctx context.Context, cwd, program string) shell.Result {
	return f(ctx, cwd, program)
}

// WithShellRunner replaces local shell execution, primarily for tests.
func WithShellRunner(runner ShellRunner) Option {
	return func(i *Interpreter) {
		if runner != nil {
			i.runShell = runner
		}
	}
}

func (*interpreterModel) submitShell(raw string, statement promptlang.Shell) instructionSequence {
	return shellSubmissionSequence(raw, statement.Program, statement.Program)
}

func (m *interpreterModel) submitCommandDefinition(
	raw string,
	definition promptlang.CommandDefinition,
) instructionSequence {
	sequence := acceptedInputSequence(raw)
	if err := m.commands.Define(definition); err != nil {
		sequence = append(sequence, publishEventInstruction{event: SubmissionFailed{
			Raw: raw, Operation: "define /" + definition.Name,
			Code: submissionErrorCode(err), Err: err,
		}})
		return sequence
	}
	sequence = append(sequence,
		appendRecordInstruction{record: room.Record{Kind: room.KindSystem, Text: "[defined] /" + definition.Name}},
		publishSnapshotInstruction{},
		publishEventInstruction{event: SubmissionSucceeded{Raw: raw}},
	)
	return sequence
}

func (m *interpreterModel) submitCommandInvocation(
	raw string,
	invocation promptlang.CommandInvocation,
) instructionSequence {
	body, err := m.commands.Resolve(invocation)
	if err != nil {
		return instructionSequence{
			publishEventInstruction{event: UnknownCommand{Raw: raw, Name: invocation.Name}},
		}
	}
	return shellSubmissionSequence(raw, "/"+invocation.Name, body.Program)
}

func shellSubmissionSequence(raw, command, program string) instructionSequence {
	return append(acceptedInputSequence(raw), startUserShellInstruction{
		raw: raw, command: command, program: program,
	})
}

func (i *Interpreter) startShell(raw, command, program string) {
	i.shellWG.Add(1)
	go func() {
		defer i.shellWG.Done()
		result := i.runShell.Run(i.lifetime, i.cwd, program)
		i.enqueue(shellCompletedOperation{command: command, result: result})
	}()
	i.publish(SubmissionSucceeded{Raw: raw})
}

func (op shellCompletedOperation) apply(i *Interpreter) {
	i.runner.Run(i.model.ApplyShellResult(op.command, i.cwd, op.result))
}

func formatShellResult(result shell.Result) string {
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
