package interpreter

import (
	"strings"

	"github.com/roomscript/coderoom/internal/promptlang"
	"github.com/roomscript/coderoom/internal/room"
	"github.com/roomscript/coderoom/internal/shell"
)

const shellRecordAlias = "shell"

type shellCompletedOperation struct {
	command string
	result  shell.Result
}

func (e *interpreterExecutor) setShellRunner(runner ShellRunner) {
	e.runShell = runner
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

func (e *interpreterExecutor) startShell(raw, command, program string) {
	e.shellWG.Add(1)
	go func() {
		defer e.shellWG.Done()
		result := e.runShell.Run(e.lifetime, e.cwd, program)
		e.enqueue(shellCompletedOperation{command: command, result: result})
	}()
	e.publish(SubmissionSucceeded{Raw: raw})
}

func (op shellCompletedOperation) apply(e *interpreterExecutor) {
	e.runner.Run(e.model.ApplyShellResult(op.command, e.cwd, op.result))
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
