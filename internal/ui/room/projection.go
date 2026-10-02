package room

import (
	roomstate "github.com/trigosec/coderoom/internal/room"
)

// NewPresenter creates a room without a session observer or a room actor.
func NewPresenter(colorByAlias func(string) string, departedColor string) Model {
	m := newPresentationModel(colorByAlias, departedColor)
	m.projectionIndices = make(map[int]int)
	return m
}

// ApplyTranscript presents ordered canonical changes. The index table only
// translates canonical indices around local UI notices; it never chooses or
// reconciles application state. Repeated versions have no effect.
func (m Model) ApplyTranscript(delta roomstate.Delta) Model {
	if delta.Version <= m.projectionVersion {
		return m
	}
	m.projectionVersion = delta.Version
	updates := make([]roomstate.IndexedRecord, 0, len(delta.RecordUpdates))
	for _, update := range delta.RecordUpdates {
		index, exists := m.projectionIndices[update.Index]
		if !exists {
			index = len(m.presentation.Records)
			m.projectionIndices[update.Index] = index
			m.presentation.Records = append(m.presentation.Records, update.Record)
		} else {
			m.presentation.Records[index] = update.Record
		}
		updates = append(updates, roomstate.IndexedRecord{Index: index, Record: update.Record})
	}
	m.presentation.Departed = delta.Meta.Departed
	m.presentation.OpenStreams = append([]roomstate.OpenStream(nil), delta.Meta.OpenStreams...)
	for index := range m.presentation.OpenStreams {
		stream := &m.presentation.OpenStreams[index]
		stream.RecordIdx = m.projectionIndices[stream.RecordIdx]
	}
	m.history = m.history.ApplyRoomDelta(roomstate.Delta{RecordUpdates: updates, Meta: roomstate.DeltaMeta{Departed: m.presentation.Departed, OpenStreams: m.presentation.OpenStreams}})
	return m.syncHistoryFollowAnchor()
}

func (m Model) appendPresentationRecord(record roomstate.Record) Model {
	index := len(m.presentation.Records)
	m.presentation.Records = append(m.presentation.Records, record)
	m.history = m.history.ApplyRoomDelta(roomstate.Delta{RecordUpdates: []roomstate.IndexedRecord{{Index: index, Record: record}}, Meta: roomstate.DeltaMeta{Departed: m.presentation.Departed, OpenStreams: m.presentation.OpenStreams}})
	return m.syncHistoryFollowAnchor()
}

// RefreshColors repaints records after the roster's participant colors change.
func (m Model) RefreshColors() Model {
	m.history = m.history.RebuildColors()
	return m.syncHistoryFollowAnchor()
}
