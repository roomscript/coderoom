package interpreter

import (
	"github.com/trigosec/coderoom/internal/agent"
	roomstate "github.com/trigosec/coderoom/internal/room"
)

// CommandDetails is detached command output for transcript presentation.
type CommandDetails struct {
	Command  string
	Cwd      string
	Output   string
	ExitCode *int
}

// FileChange is one file change presented in a transcript.
type FileChange struct {
	Path       string
	Diff       string
	ChangeKind string
}

// FileChangeDetails is detached file-change output for transcript presentation.
type FileChangeDetails struct {
	Status  string
	Changes []FileChange
}

// CommandFromRecord returns detached command details from a canonical record.
func CommandFromRecord(record roomstate.Record) (CommandDetails, bool) {
	if record.Msg == nil {
		return CommandDetails{}, false
	}
	command, ok := record.Msg.Content.(agent.Command)
	if !ok {
		return CommandDetails{}, false
	}
	details := CommandDetails{Command: command.Command, Cwd: command.Cwd, Output: command.Output}
	if command.ExitCode != nil {
		code := *command.ExitCode
		details.ExitCode = &code
	}
	return details, true
}

// FileChangesFromRecord returns detached file-change details from a canonical record.
func FileChangesFromRecord(record roomstate.Record) (FileChangeDetails, bool) {
	if record.Msg == nil {
		return FileChangeDetails{}, false
	}
	changes, ok := record.Msg.Content.(agent.FileChangeSet)
	if !ok {
		return FileChangeDetails{}, false
	}
	details := FileChangeDetails{Status: string(changes.Status), Changes: make([]FileChange, len(changes.Changes))}
	for index, change := range changes.Changes {
		details.Changes[index] = FileChange{Path: change.Path, Diff: change.Diff, ChangeKind: change.ChangeKind}
	}
	return details, true
}
