package interpreter

import "github.com/roomscript/coderoom/internal/room"

// TranscriptChanged carries canonical record changes in interpreter processing
// order. Snapshots are for state inspection; front ends render this event stream
// rather than echoing semantic events or reconciling snapshot records.
type TranscriptChanged struct{ Delta room.Delta }

func (TranscriptChanged) interpreterEvent() {}

type transcriptObserver struct{ model *interpreterModel }

func (o transcriptObserver) OnRoomUpdate(_ room.Update) {
	m := o.model
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
