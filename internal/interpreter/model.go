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
	source := m.workflows.stage.statement()
	sequence, ok := m.workflows.stage.interruptAndDispatch()
	return withSubmissionSource(sequence, source), ok
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
		return withSubmissionSource(sequence, outcome.statement)
	}
	sequence = append(sequence, publishEventInstruction{event: SubmissionSucceeded{Raw: outcome.raw}})
	return withSubmissionSource(sequence, outcome.statement)
}

func participantsResultSequence(outcome participantsResult) instructionSequence {
	return withSubmissionSource(instructionSequence{
		publishSnapshotInstruction{},
		publishEventInstruction{event: ParticipantsListed{
			Participants: append([]participant.View(nil), outcome.participants...),
		}},
		publishEventInstruction{event: SubmissionSucceeded{Raw: outcome.raw}},
	}, outcome.statement)
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
	return body.Value, nil
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
