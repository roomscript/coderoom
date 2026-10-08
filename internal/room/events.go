package room

import (
	"fmt"
	"slices"

	"github.com/roomscript/coderoom/internal/agent"
	"github.com/roomscript/coderoom/internal/session"
)

func (r *Room) applyEvent(e session.Event) (Update, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if msg, ok := e.(session.AgentMessage); ok {
		dirty := r.handleAgentMessageLocked(msg.Alias, msg.Msg)
		update := r.bumpVersionLocked(dirty...)
		if msg.TurnCompleted {
			update.Trigger = UpdateTrigger{
				Kind:     UpdateTriggerAgentTurnCompleted,
				Alias:    msg.Alias,
				StreamID: msg.Msg.StreamID,
				TurnID:   msg.TurnID,
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
		return r.appendSystemRecordLocked("[" + e.Alias + " starting]"), true
	case session.AgentReady:
		r.members[e.Alias] = struct{}{}
		delete(r.departed, e.Alias)
		dirty := r.refreshLatestHandoffSourceLocked(e.Alias)
		dirty = append(dirty, r.appendSystemRecordLocked("["+e.Alias+" joined]")...)
		slices.Sort(dirty)
		return slices.Compact(dirty), true
	case session.AgentStopped:
		dirty := r.handleDepartureLocked(e.Alias)
		dirty = append(dirty, r.appendSystemRecordLocked("["+e.Alias+" left]")...)
		return slices.Compact(dirty), true
	case session.AgentCrashed:
		dirty := r.handleDepartureLocked(e.Alias)
		dirty = append(dirty, r.appendSystemRecordLocked("["+e.Alias+" crashed]")...)
		return slices.Compact(dirty), true
	case session.AgentLog:
		return r.appendRecordLocked(Record{Kind: KindLog, Alias: e.Alias, Text: e.Text}), true
	case session.ContextHandoff:
		return r.appendSystemRecordLocked(handoffPreview(e)), true
	default:
		return nil, false
	}
}

func (r *Room) handleDepartureLocked(alias string) []int {
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

func handoffPreview(e session.ContextHandoff) string {
	if e.Preview != "" {
		return e.Preview
	}
	return fmt.Sprintf("[handoff %s -> %s]\n\n%s", e.FromAlias, e.ToAlias, e.Text)
}

func (r *Room) handleAgentMessageLocked(alias string, msg agent.Message) []int {
	switch msg.Content.(type) {
	case agent.Output, agent.Reasoning, agent.Command, agent.FileChangeSet:
		if msg.Mode == agent.ModeFlush {
			r.sealStreamLocked(msg.StreamID)
			return r.refreshHandoffSourceAfterMessageLocked(alias, msg)
		}
	}

	if slot, ok := r.streaming[msg.StreamID]; ok {
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
	r.streaming[msg.StreamID] = OpenStream{
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

func (r *Room) refreshHandoffSourceAfterMessageLocked(alias string, msg agent.Message) []int {
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

func (r *Room) clearStreamsLocked(alias string) {
	for streamID, slot := range r.streaming {
		if slot.Alias == alias {
			delete(r.streaming, streamID)
		}
	}
}

func (r *Room) sealStreamLocked(streamID agent.StreamID) {
	delete(r.streaming, streamID)
}
