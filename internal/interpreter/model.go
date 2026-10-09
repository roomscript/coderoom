package interpreter

import (
	"fmt"

	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/promptlang"
	"github.com/roomscript/coderoom/internal/room"
	"github.com/roomscript/coderoom/internal/session"
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

func (m *interpreterModel) prepareCommand(raw string, statement promptlang.Statement) (instructionSequence, bool) {
	for _, definition := range nativeCommandDefinitions {
		if definition.matches(statement) && definition.submit != nil {
			return definition.submit(m, raw, statement), true
		}
	}
	return nil, false
}

func sessionSubmissionSequence(raw, operation string, command session.Command) instructionSequence {
	return append(acceptedInputSequence(raw), executeCommandInstruction{
		command: command,
		outcome: submissionOutcome{raw: raw, operation: operation},
	})
}

func submissionResultSequence(outcome submissionOutcome) instructionSequence {
	sequence := instructionSequence{publishSnapshotInstruction{}}
	if outcome.err != nil {
		sequence = append(sequence, publishEventInstruction{event: SubmissionFailed{
			Raw: outcome.raw, Operation: outcome.operation,
			Code: ErrorExecutionFailed, Err: outcome.err,
		}})
		return sequence
	}
	sequence = append(sequence, publishEventInstruction{event: SubmissionSucceeded{Raw: outcome.raw}})
	return sequence
}

func rosterResultSequence(outcome rosterResult) instructionSequence {
	return instructionSequence{
		publishSnapshotInstruction{},
		publishEventInstruction{event: RosterListed{
			Participants: append([]participant.View(nil), outcome.participants...),
		}},
		publishEventInstruction{event: SubmissionSucceeded{Raw: outcome.raw}},
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
