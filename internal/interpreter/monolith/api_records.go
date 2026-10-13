package monolith

import (
	"github.com/roomscript/coderoom/internal/interpreter/api"
	"github.com/roomscript/coderoom/internal/room"
)

// CommandFromRecord returns detached command details from a canonical record.
func CommandFromRecord(record room.Record) (CommandDetails, bool) {
	return api.CommandFromRecord(record)
}

// FileChangesFromRecord returns detached file-change details from a canonical record.
func FileChangesFromRecord(record room.Record) (FileChangeDetails, bool) {
	return api.FileChangesFromRecord(record)
}
