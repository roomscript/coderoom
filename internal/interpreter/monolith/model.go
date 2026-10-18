package monolith

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
	transcriptVersion    uint64
	transcriptChanges    []TranscriptChanged
	room                 *room.Room
	commands             *promptlang.Registry
	approval             *Approval
	onCompletedExecution func()
}

type modelSnapshot struct {
	room     room.Snapshot
	approval *Approval
}

func newInterpreterModel() *interpreterModel {
	model := &interpreterModel{commands: promptlang.NewRegistry()}
	model.room = room.New(room.WithObserver(transcriptObserver{model: model}))
	return model
}

func (m *interpreterModel) AppendRecord(record room.Record) {
	m.room.AppendRecord(record)
}

func (m *interpreterModel) ResolveCommand(invocation promptlang.UserCommand) (promptlang.Shell, error) {
	definition, err := m.commands.Resolve(invocation)
	if err != nil {
		return promptlang.Shell{}, fmt.Errorf("resolve command: %w", err)
	}
	body, err := promptlang.RequireShell(definition)
	if err != nil {
		return promptlang.Shell{}, fmt.Errorf("resolve command: %w", err)
	}
	return body.Value, nil
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

// LatestOutput reads completed canonical output for the exact participant instance.
func (m *interpreterModel) LatestOutput(alias participant.Alias) (session.HandoffSource, bool) {
	return m.room.LatestHandoffSource(alias)
}

// Room exposes canonical room state to the temporary executor bridge.
func (m *interpreterModel) Room() *room.Room { return m.room }
