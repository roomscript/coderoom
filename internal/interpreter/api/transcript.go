package api

import "github.com/roomscript/coderoom/internal/room"

// TranscriptChanged carries canonical record changes in interpreter processing
// order. Snapshots are for state inspection; front ends render this event stream
// rather than echoing semantic events or reconciling snapshot records.
type TranscriptChanged struct{ Delta room.Delta }

func (TranscriptChanged) interpreterEvent() {}
