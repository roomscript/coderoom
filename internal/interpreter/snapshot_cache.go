package interpreter

import (
	"sync/atomic"

	"github.com/roomscript/coderoom/internal/agent"
	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/room"
)

// snapshotCache stores the latest immutable snapshot published by the
// operation loop. Readers receive detached copies and never access live model
// state.
type snapshotCache struct {
	value atomic.Pointer[Snapshot]
}

func (c *snapshotCache) Store(snapshot Snapshot) {
	detached := cloneSnapshot(snapshot)
	c.value.Store(&detached)
}

func (c *snapshotCache) Load() Snapshot {
	snapshot := c.value.Load()
	if snapshot == nil {
		return Snapshot{}
	}
	return cloneSnapshot(*snapshot)
}

func cloneSnapshot(source Snapshot) Snapshot {
	return Snapshot{
		Room:         cloneRoomSnapshot(source.Room),
		Participants: append([]participant.View(nil), source.Participants...),
		Approval:     cloneApproval(source.Approval),
		Stage:        cloneStagedSubmission(source.Stage),
	}
}

func cloneStagedSubmission(source *StagedSubmission) *StagedSubmission {
	if source == nil {
		return nil
	}
	clone := *source
	clone.Routing = append([]string(nil), source.Routing...)
	clone.NotReadyAliases = append([]string(nil), source.NotReadyAliases...)
	clone.Interruptible = append([]string(nil), source.Interruptible...)
	clone.Unavailable = append([]string(nil), source.Unavailable...)
	return &clone
}

func cloneRoomSnapshot(source room.Snapshot) room.Snapshot {
	departed := make(map[string]bool, len(source.Departed))
	for alias, value := range source.Departed {
		departed[alias] = value
	}
	records := make([]room.Record, len(source.Records))
	for index, record := range source.Records {
		records[index] = cloneRoomRecord(record)
	}
	return room.Snapshot{
		RoomID:      source.RoomID,
		Version:     source.Version,
		Members:     append([]string(nil), source.Members...),
		Departed:    departed,
		Records:     records,
		OpenStreams: append([]room.OpenStream(nil), source.OpenStreams...),
	}
}

func cloneRoomRecord(source room.Record) room.Record {
	record := source
	record.Routing = append([]string(nil), source.Routing...)
	record.FailedRouting = append([]string(nil), source.FailedRouting...)
	record.UnsentRouting = append([]string(nil), source.UnsentRouting...)
	if source.Msg != nil {
		message := *source.Msg
		message.Content = cloneMessageContent(source.Msg.Content)
		record.Msg = &message
	}
	return record
}

func cloneMessageContent(content agent.MessageContent) agent.MessageContent {
	switch content := content.(type) {
	case agent.Command:
		if content.ExitCode != nil {
			exitCode := *content.ExitCode
			content.ExitCode = &exitCode
		}
		return content
	case agent.FileChangeSet:
		content.Changes = append([]agent.FileChange(nil), content.Changes...)
		return content
	default:
		return content
	}
}
