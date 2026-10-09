package interpreter

import (
	"github.com/roomscript/coderoom/internal/agent"
	"github.com/roomscript/coderoom/internal/promptlang"
	"github.com/roomscript/coderoom/internal/room"
	"github.com/roomscript/coderoom/internal/shell"
)

// Shell requests resolve a program, accept input, then launch asynchronous work.
// Command definitions only update the registry; invocation resolves before acceptance.
func (*interpreterModel) prepareShell(raw string, statement promptlang.Shell) instructionSequence {
	return prepareShellExecution(raw, statement.Program.Value, statement.Program.Value)
}

func (m *interpreterModel) defineShellCommand(
	raw string,
	definition promptlang.CommandDefinition,
) instructionSequence {
	sequence := acceptedInputSequence(raw)
	if err := m.commands.Define(definition); err != nil {
		sequence = append(sequence, publishEventInstruction{event: SubmissionFailed{
			Raw: raw, Operation: "define /" + definition.Name.Value,
			Code: submissionErrorCode(err), Err: err,
		}})
		return sequence
	}
	sequence = append(sequence,
		appendRecordInstruction{record: room.Record{Kind: room.KindSystem, Text: "[defined] /" + definition.Name.Value}},
		publishSnapshotInstruction{},
		publishEventInstruction{event: SubmissionSucceeded{Raw: raw}},
	)
	return sequence
}

func (m *interpreterModel) prepareShellCommand(
	raw string,
	invocation promptlang.CommandInvocation,
) instructionSequence {
	body, err := m.commands.Resolve(invocation)
	if err != nil {
		return instructionSequence{
			publishEventInstruction{event: UnknownCommand{Raw: raw, Name: invocation.Name.Value, Err: err}},
		}
	}
	return prepareShellExecution(raw, "/"+invocation.Name.Value, body.Value.Program.Value)
}

func prepareShellExecution(raw, command, program string) instructionSequence {
	return append(acceptedInputSequence(raw), startUserShellInstruction{
		raw: raw, command: command, program: program,
	})
}

// startShell launches work and returns control. Submission success means the
// launch was accepted; a later shellCompletedOperation reports execution results.
func (e *interpreterExecutor) startShell(raw, command, program string, statement promptlang.ParsedStatement) {
	e.launchUserShell(raw, command, program, statement)
	e.publish(SubmissionSucceeded{Raw: raw, Statement: statement})
}

// ApplyShellResult resumes observation after asynchronous work completes: record
// the command outcome, publish its structured result, then publish updated state.
func (m *interpreterModel) ApplyShellResult(raw, command, cwd string, result shell.Result, statement promptlang.ParsedStatement) instructionSequence {
	output := formatShellResult(result)
	return withSubmissionSource(instructionSequence{
		appendRecordInstruction{record: room.NewAgentRecord(shellRecordAlias, agent.Message{
			Mode: agent.ModeSingle,
			Content: agent.Command{
				Command: command, Cwd: cwd, Output: output, ExitCode: result.ExitCode,
			},
		})},
		publishEventInstruction{event: ShellCompleted{
			Raw: raw, Command: command, Cwd: cwd, Result: result, Output: output,
		}},
		publishSnapshotInstruction{},
	}, statement)
}
