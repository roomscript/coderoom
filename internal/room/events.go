package room

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/roomscript/coderoom/internal/agent"
	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/session"
)

func (r *Room) applyEvent(e session.Event) (Update, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if msg, ok := e.(session.AgentMessage); ok {
		dirty := r.handleAgentMessageLocked(msg.Alias, msg.Msg)
		update := r.bumpVersionLocked(dirty...)
		if msg.WorkCompleted {
			r.recordCompletedExecutionLocked(msg.Alias, msg.ExecutionID)
			update.Trigger = UpdateTrigger{
				Kind:        UpdateTriggerAgentWorkCompleted,
				Alias:       msg.Alias,
				StreamID:    msg.Msg.StreamID,
				ExecutionID: msg.ExecutionID,
			}
		}
		return update, true
	}

	dirty, ok := r.handleLifecycleEventLocked(e)
	if !ok {
		return Update{}, false
	}
	update := r.bumpVersionLocked(dirty...)
	return update, true
}

func (r *Room) handleLifecycleEventLocked(e session.Event) ([]int, bool) {
	switch e := e.(type) {
	case session.AgentStarting:
		return r.appendSystemRecordLocked(startingNotice(e)), true
	case session.AgentReady:
		return r.handleJoinLocked(e.Alias)
	case session.AgentStopped:
		return r.handleLeaveLocked(e.Alias, "left"), true
	case session.AgentCrashed:
		return r.handleLeaveLocked(e.Alias, "crashed"), true
	case session.AgentLog:
		return r.appendRecordLocked(Record{Kind: KindLog, Alias: e.Alias, Text: e.Text}), true
	case session.HandoffDelivered:
		return r.appendSystemRecordLocked(handoffPreview(e)), true
	case session.ParticipantConfigChanged:
		return r.appendSystemRecordLocked(configChangedNotice(e.Sources)), true
	case session.BackendUnavailable:
		return r.appendSystemRecordLocked("[" + e.Backend + " unavailable: " + e.Err.Error() + "]"), true
	default:
		return nil, false
	}
}

func (r *Room) handleJoinLocked(alias participant.Alias) ([]int, bool) {
	if r.departed[alias] {
		return nil, false
	}
	r.members[alias] = struct{}{}
	dirty := r.refreshLatestHandoffSourceLocked(alias)
	dirty = append(dirty, r.appendSystemRecordLocked("["+alias.Name+" joined]")...)
	slices.Sort(dirty)
	return slices.Compact(dirty), true
}

// handleLeaveLocked records a departure; verb is "left" or "crashed".
func (r *Room) handleLeaveLocked(alias participant.Alias, verb string) []int {
	dirty := r.handleDepartureLocked(alias)
	dirty = append(dirty, r.appendSystemRecordLocked("["+alias.Name+" "+verb+"]")...)
	return slices.Compact(dirty)
}

func (r *Room) handleDepartureLocked(alias participant.Alias) []int {
	delete(r.members, alias)
	r.clearStreamsLocked(alias)
	r.departed[alias] = true
	return r.refreshLatestHandoffSourceLocked(alias)
}

func (r *Room) appendSystemRecordLocked(text string) []int {
	return r.appendRecordLocked(Record{Kind: KindSystem, Text: text})
}

func (r *Room) appendRecordLocked(record Record) []int {
	idx := len(r.records)
	r.records = append(r.records, record)
	return []int{idx}
}

func startingNotice(e session.AgentStarting) string {
	if e.Backend == "" {
		return "[" + e.Alias.Name + " starting]"
	}
	return "[" + e.Alias.Name + " starting · " + e.Backend + "]"
}

// configChangedNotice tells the user that edited config files only apply
// after a restart.
func configChangedNotice(sources []string) string {
	names := make([]string, len(sources))
	for i, src := range sources {
		names[i] = filepath.Base(src)
	}
	if len(names) == 1 {
		return "[" + names[0] + " changed since startup; using the loaded version. Restart coderoom to apply it]"
	}
	return "[" + strings.Join(names, ", ") + " changed since startup; using the loaded versions. Restart coderoom to apply them]"
}

func handoffPreview(e session.HandoffDelivered) string {
	if e.Preview != "" {
		return e.Preview
	}
	return fmt.Sprintf("[handoff %s -> %s]\n\n%s", e.FromAlias.Name, e.ToAlias.Name, e.Text)
}

func (r *Room) handleAgentMessageLocked(alias participant.Alias, msg agent.Message) []int {
	switch msg.Content.(type) {
	case agent.Output, agent.Reasoning, agent.Command, agent.FileChangeSet:
		if msg.Mode == agent.ModeFlush {
			r.sealStreamLocked(alias, msg.StreamID)
			return r.refreshHandoffSourceAfterMessageLocked(alias, msg)
		}
	}

	if slot, ok := r.streaming[alias][msg.StreamID]; ok {
		updated, err := r.records[slot.RecordIdx].Accumulate(msg)
		if err == nil {
			r.records[slot.RecordIdx] = updated
			dirty := []int{slot.RecordIdx}
			dirty = append(dirty, r.refreshHandoffSourceAfterMessageLocked(alias, msg)...)
			slices.Sort(dirty)
			return slices.Compact(dirty)
		}
	}

	idx := len(r.records)
	r.records = append(r.records, NewAgentRecord(alias, msg))
	if r.departed[alias] {
		return []int{idx}
	}
	if r.streaming[alias] == nil {
		r.streaming[alias] = make(map[agent.StreamID]OpenStream)
	}
	r.streaming[alias][msg.StreamID] = OpenStream{
		Alias:     alias,
		RecordIdx: idx,
		StreamID:  msg.StreamID,
		Kind:      openStreamKind(msg),
	}
	dirty := []int{idx}
	dirty = append(dirty, r.refreshHandoffSourceAfterMessageLocked(alias, msg)...)
	slices.Sort(dirty)
	return slices.Compact(dirty)
}

func (r *Room) refreshHandoffSourceAfterMessageLocked(alias participant.Alias, msg agent.Message) []int {
	if _, ok := msg.Content.(agent.Output); !ok {
		return nil
	}
	if msg.Mode == agent.ModeStream {
		return nil
	}
	return r.refreshLatestHandoffSourceLocked(alias)
}

func openStreamKind(msg agent.Message) OpenStreamKind {
	switch msg.Content.(type) {
	case agent.Reasoning:
		return OpenStreamReasoning
	case agent.Command:
		return OpenStreamCommand
	case agent.FileChangeSet:
		return OpenStreamFileChange
	default:
		return OpenStreamOutput
	}
}

func (r *Room) clearStreamsLocked(alias participant.Alias) {
	delete(r.streaming, alias)
}

func (r *Room) sealStreamLocked(alias participant.Alias, streamID agent.StreamID) {
	delete(r.streaming[alias], streamID)
	if len(r.streaming[alias]) == 0 {
		delete(r.streaming, alias)
	}
}
