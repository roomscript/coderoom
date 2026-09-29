package interpreter

import (
	"fmt"

	"github.com/trigosec/coderoom/internal/agent"
	"github.com/trigosec/coderoom/internal/participant"
	"github.com/trigosec/coderoom/internal/promptlang"
	"github.com/trigosec/coderoom/internal/room"
	"github.com/trigosec/coderoom/internal/session"
	"github.com/trigosec/coderoom/internal/shell"
)

// interpreterModel owns mutable interpreter state and deterministic decisions.
// It is confined to the serialized interpreter operation loop.
type interpreterModel struct {
	room         *room.Room
	commands     *promptlang.Registry
	workflows    workflowCollection
	approval     *Approval
	stagePending bool
}

type modelSnapshot struct {
	room     room.Snapshot
	approval *Approval
}

func (m *interpreterModel) PreflightSubmission(raw string) instructionSequence {
	if !m.stagePending {
		return nil
	}
	return instructionSequence{publishEventInstruction{event: InputRejected{
		Raw: raw, Code: ErrorStagePending, Err: ErrStagePending,
	}}}
}

func newInterpreterModel() *interpreterModel {
	return &interpreterModel{
		room:     room.New(),
		commands: promptlang.NewRegistry(),
	}
}

func (m *interpreterModel) Submit(
	raw string,
	statement promptlang.Statement,
	fallback session.Command,
) instructionSequence {
	if sequence, handled := m.workflows.submit(raw, statement, m.commands); handled {
		return sequence
	}
	if sequence, handled := m.submitCommand(raw, statement); handled {
		return sequence
	}
	if fallback != nil {
		return sessionSubmissionSequence(raw, submissionOperation(statement), fallback)
	}
	return instructionSequence{publishEventInstruction{event: UnknownCommand{
		Raw: raw, Name: commandName(statement),
	}}}
}

func (m *interpreterModel) submitCommand(
	raw string,
	statement promptlang.Statement,
) (instructionSequence, bool) {
	if sequence, handled := m.submitSessionCommand(raw, statement); handled {
		return sequence, true
	}
	if sequence, handled := m.submitShellCommand(raw, statement); handled {
		return sequence, true
	}
	return m.submitControlCommand(raw, statement)
}

func (m *interpreterModel) submitSessionCommand(
	raw string,
	statement promptlang.Statement,
) (instructionSequence, bool) {
	switch statement := statement.(type) {
	case promptlang.Invite:
		return m.submitInvite(raw, statement), true
	case promptlang.Remove:
		return m.submitRemove(raw, statement), true
	case promptlang.Cancel:
		return m.submitCancel(raw, statement), true
	case promptlang.PolicyEnable:
		return m.submitPolicyEnable(raw, statement), true
	default:
		return nil, false
	}
}

func (m *interpreterModel) submitShellCommand(
	raw string,
	statement promptlang.Statement,
) (instructionSequence, bool) {
	switch statement := statement.(type) {
	case promptlang.Shell:
		return m.submitShell(raw, statement), true
	case promptlang.CommandDefinition:
		return m.submitCommandDefinition(raw, statement), true
	case promptlang.CommandInvocation:
		return m.submitCommandInvocation(raw, statement), true
	default:
		return nil, false
	}
}

func (m *interpreterModel) submitControlCommand(
	raw string,
	statement promptlang.Statement,
) (instructionSequence, bool) {
	switch statement.(type) {
	case promptlang.Who:
		return m.submitWho(raw), true
	case promptlang.Help:
		return m.submitHelp(raw), true
	case promptlang.Quit:
		return m.submitQuit(raw), true
	default:
		return nil, false
	}
}

func sessionSubmissionSequence(raw, operation string, command session.Command) instructionSequence {
	return append(acceptedInputSequence(raw), executeCommandInstruction{
		command:    command,
		completion: submissionCompletion{raw: raw, operation: operation},
	})
}

func (m *interpreterModel) ApplySessionEvent(event session.Event) (instructionSequence, bool) {
	if !m.applyApprovalEvent(event) {
		return nil, false
	}
	m.room.ApplyEvent(event)
	return m.workflows.applySessionEvent(event), true
}

func (m *interpreterModel) ApplyCompletion(completion workflowCompletion) instructionSequence {
	switch completion := completion.(type) {
	case submissionCompletion:
		return submissionResultSequence(completion)
	case rosterCompletion:
		return rosterResultSequence(completion)
	default:
		return m.workflows.applyCompletion(completion)
	}
}

func submissionResultSequence(completion submissionCompletion) instructionSequence {
	sequence := instructionSequence{publishSnapshotInstruction{}}
	if completion.err != nil {
		sequence = append(sequence, publishEventInstruction{event: SubmissionFailed{
			Raw: completion.raw, Operation: completion.operation,
			Code: ErrorExecutionFailed, Err: completion.err,
		}})
		return sequence
	}
	sequence = append(sequence, publishEventInstruction{event: SubmissionSucceeded{Raw: completion.raw}})
	return sequence
}

func rosterResultSequence(completion rosterCompletion) instructionSequence {
	return instructionSequence{
		publishSnapshotInstruction{},
		publishEventInstruction{event: RosterListed{
			Participants: append([]participant.View(nil), completion.participants...),
		}},
		publishEventInstruction{event: SubmissionSucceeded{Raw: completion.raw}},
	}
}

func (m *interpreterModel) ApplyShellResult(command, cwd string, result shell.Result) instructionSequence {
	output := formatShellResult(result)
	return instructionSequence{
		appendRecordInstruction{record: room.NewAgentRecord(shellRecordAlias, agent.Message{
			Mode: agent.ModeSingle,
			Content: agent.Command{
				Command: command, Cwd: cwd, Output: output, ExitCode: result.ExitCode,
			},
		})},
		publishEventInstruction{event: ShellCompleted{
			Command: command, Cwd: cwd, Result: result, Output: output,
		}},
		publishSnapshotInstruction{},
	}
}

func (m *interpreterModel) AppendRecord(record room.Record) {
	m.room.AppendRecord(record)
}

func (m *interpreterModel) ResolveCommand(invocation promptlang.CommandInvocation) (promptlang.Shell, error) {
	body, err := m.commands.Resolve(invocation)
	if err != nil {
		return promptlang.Shell{}, fmt.Errorf("resolve command: %w", err)
	}
	return body, nil
}

func (m *interpreterModel) Snapshot() modelSnapshot {
	return modelSnapshot{
		room:     m.room.Snapshot(),
		approval: cloneApproval(m.approval),
	}
}

func (m *interpreterModel) Close() {
	m.room.Close()
}
