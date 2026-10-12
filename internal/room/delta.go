package room

import (
	"errors"

	"github.com/roomscript/coderoom/internal/participant"
)

// ErrResyncRequired reports that the caller's local room version cannot be
// advanced incrementally and must be refreshed from Snapshot().
var ErrResyncRequired = errors.New("room delta requires resync")

const deltaHistoryLimit = 1024

// IndexedRecord identifies one room record by its current append-only index.
type IndexedRecord struct {
	Index  int
	Record Record
}

// DeltaMeta is the bounded full-state metadata returned with every room delta.
type DeltaMeta struct {
	Members     []participant.Alias
	Departed    map[participant.Alias]bool
	OpenStreams []OpenStream
}

// Delta is an incremental room update: bounded metadata in full plus the
// record updates since a caller-supplied version.
type Delta struct {
	RoomID        ID
	Version       uint64
	Meta          DeltaMeta
	RecordUpdates []IndexedRecord
}

// Delta returns the record changes since fromVersion. Callers must bootstrap
// with Snapshot() first and resnapshot when ErrResyncRequired is returned.
func (r *Room) Delta(fromVersion uint64) (Delta, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if fromVersion == 0 || fromVersion > r.version || !r.canBuildDeltaLocked(fromVersion) {
		return Delta{}, ErrResyncRequired
	}

	delta := Delta{
		RoomID:  r.id,
		Version: r.version,
		Meta: DeltaMeta{
			Members:     clonedMembers(r.members),
			Departed:    clonedDeparted(r.departed),
			OpenStreams: clonedOpenStreams(r.streaming),
		},
	}

	if fromVersion == r.version {
		return delta, nil
	}

	indices := r.deltaRecordIndicesLocked(fromVersion)
	delta.RecordUpdates = make([]IndexedRecord, len(indices))
	for i, idx := range indices {
		delta.RecordUpdates[i] = IndexedRecord{Index: idx, Record: cloneRecord(r.records[idx])}
	}
	return delta, nil
}

func (r *Room) canBuildDeltaLocked(fromVersion uint64) bool {
	for version := fromVersion + 1; version <= r.version; version++ {
		if _, ok := r.dirty[version]; !ok {
			return false
		}
	}
	return true
}

func (r *Room) deltaRecordIndicesLocked(fromVersion uint64) []int {
	var dirty []int
	for version := fromVersion + 1; version <= r.version; version++ {
		dirty = append(dirty, r.dirty[version]...)
	}
	return uniqueRecordIndices(dirty)
}
