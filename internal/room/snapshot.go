package room

import (
	"slices"

	"github.com/roomscript/coderoom/internal/agent"
)

// Snapshot is a detached, point-in-time copy of a room's full state.
type Snapshot struct {
	RoomID      ID
	Version     uint64
	Members     []string
	Departed    map[string]bool
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

func clonedMembers(members map[string]struct{}) []string {
	out := make([]string, 0, len(members))
	for alias := range members {
		out = append(out, alias)
	}
	slices.Sort(out)
	return out
}

func clonedDeparted(departed map[string]bool) map[string]bool {
	out := make(map[string]bool, len(departed))
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

func clonedOpenStreams(streaming map[agent.StreamID]OpenStream) []OpenStream {
	streamIDs := make([]agent.StreamID, 0, len(streaming))
	for streamID := range streaming {
		streamIDs = append(streamIDs, streamID)
	}
	slices.Sort(streamIDs)

	openStreams := make([]OpenStream, 0, len(streamIDs))
	for _, streamID := range streamIDs {
		openStreams = append(openStreams, streaming[streamID])
	}
	return openStreams
}
