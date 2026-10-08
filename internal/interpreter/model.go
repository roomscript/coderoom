package interpreter

import (
	"fmt"

	"github.com/roomscript/coderoom/internal/agent"
	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/promptlang"
	"github.com/roomscript/coderoom/internal/room"
	"github.com/roomscript/coderoom/internal/session"
	"github.com/roomscript/coderoom/internal/shell"
)

// interpreterModel owns mutable interpreter state and deterministic decisions.
// It is confined to the serialized interpreter operation loop.
type interpreterModel struct {
	transcriptVersion uint64
	transcriptChanges []TranscriptChanged
	room              *room.Room
	commands          *promptlang.Registry
	workflows         workflowCollection
	approval          *Approval
}

type modelSnapshot struct {
	room     room.Snapshot
	approval *Approval
	stage    *StagedSubmission
}

func (m *interpreterModel) PreflightSubmission(raw string) instructionSequence {
	if !m.workflows.stage.pending() {
		return nil
	}
	return instructionSequence{publishEventInstruction{event: InputRejected{
		Raw: raw, Code: ErrorStagePending, Err: ErrStagePending,
	}}}
}

func (m *interpreterModel) TakeStageForEdit() (instructionSequence, string, bool) {
	return m.workflows.stage.takeForEdit()
}

func (m *interpreterModel) DiscardStage() (instructionSequence, bool) {
	return m.workflows.stage.discard()
}

func (m *interpreterModel) InterruptAndDispatchStage() (instructionSequence, bool) {
	return m.workflows.stage.interruptAndDispatch()
}

func newInterpreterModel() *interpreterModel {
	model := &interpreterModel{commands: promptlang.NewRegistry()}
	model.room = room.New(room.WithObserver(transcriptObserver{model: model}))
	return model
}

func (m *interpreterModel) Submit(
	raw string,
	statement promptlang.Statement,
) instructionSequence {
	if sequence, handled := m.submitCommand(raw, statement); handled {
		return sequence
	}
	return instructionSequence{publishEventInstruction{event: UnknownCommand{
		Raw: raw, Name: commandName(statement),
	}}}
}

func (m *interpreterModel) submitCommand(raw string, statement promptlang.Statement) (instructionSequence, bool) {
	for _, definition := range nativeCommandDefinitions {
		if definition.matches(statement) && definition.submit != nil {
			return definition.submit(m, raw, statement), true
		}
	}
	return nil, false
}

func sessionSubmissionSequence(raw, operation string, command session.Command) instructionSequence {
	return append(acceptedInputSequence(raw), executeCommandInstruction{
		command:    command,
		completion: submissionCompletion{raw: raw, operation: operation},
	})
}

func (m *interpreterModel) ApplySessionEvent(event session.Event) (instructionSequence, bool) {
	// Routing outcomes are consumed by the command completion, not a state projection.
	if _, ok := event.(session.RoutingCompleted); ok {
		return nil, false
	}
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

func (m *interpreterModel) ReadHandoffSource(alias string) (session.HandoffSource, bool) {
	return m.room.LatestHandoffSource(alias)
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
		stage:    m.workflows.stage.snapshot(),
	}
}

func (m *interpreterModel) Close() {
	m.room.Close()
}
