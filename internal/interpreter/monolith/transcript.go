package monolith

import (
	"github.com/roomscript/coderoom/internal/room"
)

type transcriptObserver struct{ model *interpreterModel }

func (o transcriptObserver) OnRoomUpdate(update room.Update) {
	m := o.model
	if update.Trigger.Kind == room.UpdateTriggerAgentWorkCompleted && m.onCompletedExecution != nil {
		m.onCompletedExecution()
	}

	delta, err := m.room.Delta(m.transcriptVersion)
	if err != nil {
		snapshot := m.room.Snapshot()
		delta = room.Delta{RoomID: snapshot.RoomID, Version: snapshot.Version,
			Meta: room.DeltaMeta{Members: snapshot.Members, Departed: snapshot.Departed, OpenStreams: snapshot.OpenStreams}}
		for index, record := range snapshot.Records {
			delta.RecordUpdates = append(delta.RecordUpdates, room.IndexedRecord{Index: index, Record: record})
		}
	}
	m.transcriptVersion = delta.Version
	m.transcriptChanges = append(m.transcriptChanges, TranscriptChanged{Delta: delta})
}

func (m *interpreterModel) TakeTranscriptChanges() []TranscriptChanged {
	changes := m.transcriptChanges
	m.transcriptChanges = nil
	return changes
}

// WithCompletedExecution forwards canonical turn-completion facts on the shared lane.
// The owner collects the fact and queues advancement after room updates, never
// executing commands inline from the room observer. Remove this bridge with the
// monolith when the interpreter owns canonical room state (checkpoint 9).
func WithCompletedExecution(handle func()) Option {
	return func(i *Interpreter) { i.model.onCompletedExecution = handle }
}
