package room

import (
	"bytes"
	"cmp"
	"slices"
	"strings"

	"github.com/roomscript/coderoom/internal/agent"
	"github.com/roomscript/coderoom/internal/participant"
)

// Snapshot is a detached, point-in-time copy of a room's full state.
type Snapshot struct {
	RoomID      ID
	Version     uint64
	Members     []participant.Alias
	Departed    map[participant.Alias]bool
	Records     []Record
	OpenStreams []OpenStream
}

// Snapshot returns a detached, point-in-time copy of the room's current
// state, safe for the caller to read without further synchronization.
func (r *Room) Snapshot() Snapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return Snapshot{
		RoomID:      r.id,
		Version:     r.version,
		Members:     clonedMembers(r.members),
		Departed:    clonedDeparted(r.departed),
		Records:     clonedRecords(r.records),
		OpenStreams: clonedOpenStreams(r.streaming),
	}
}

func clonedMembers(members map[participant.Alias]struct{}) []participant.Alias {
	out := make([]participant.Alias, 0, len(members))
	for alias := range members {
		out = append(out, alias)
	}
	slices.SortFunc(out, func(a, b participant.Alias) int {
		if a.Name != b.Name {
			return strings.Compare(a.Name, b.Name)
		}
		return bytes.Compare(a.ID[:], b.ID[:])
	})
	return out
}

func clonedDeparted(departed map[participant.Alias]bool) map[participant.Alias]bool {
	out := make(map[participant.Alias]bool, len(departed))
	for alias, isDeparted := range departed {
		out[alias] = isDeparted
	}
	return out
}

func clonedRecords(records []Record) []Record {
	out := make([]Record, len(records))
	for i, record := range records {
		out[i] = cloneRecord(record)
	}
	return out
}

func clonedOpenStreams(streaming map[participant.Alias]map[agent.StreamID]OpenStream) []OpenStream {
	streams := make([]OpenStream, 0)
	for _, owned := range streaming {
		for _, stream := range owned {
			streams = append(streams, stream)
		}
	}
	slices.SortFunc(streams, func(a, b OpenStream) int {
		if a.StreamID != b.StreamID {
			return strings.Compare(string(a.StreamID), string(b.StreamID))
		}
		return cmp.Compare(a.RecordIdx, b.RecordIdx)
	})
	return streams
}
