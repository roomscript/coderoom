package room

import (
	"github.com/roomscript/coderoom/internal/agent"
	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/session"
)

// LatestCompletedOutput returns the latest completed user-visible output text
// for alias from canonical room state.
func (r *Room) LatestCompletedOutput(alias participant.Alias) (string, bool) {
	source, ok := r.LatestHandoffSource(alias)
	if !ok {
		return "", false
	}
	return source.Text, true
}

// LatestHandoffSource returns the latest completed room-visible output record
// eligible for /handoff for alias.
func (r *Room) LatestHandoffSource(alias participant.Alias) (session.HandoffSource, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return r.latestHandoffSourceLocked(alias)
}

func (r *Room) latestHandoffSourceLocked(alias participant.Alias) (session.HandoffSource, bool) {
	if _, active := r.members[alias]; !active {
		return session.HandoffSource{}, false
	}
	for i := len(r.records) - 1; i >= 0; i-- {
		text, ok := r.handoffEligibleOutputLocked(i, alias)
		if !ok {
			continue
		}
		return session.HandoffSource{Text: text, RecordIndex: i}, true
	}
	return session.HandoffSource{}, false
}

func (r *Room) handoffEligibleOutputLocked(index int, alias participant.Alias) (string, bool) {
	if index < 0 || index >= len(r.records) {
		return "", false
	}
	record := r.records[index]
	if record.Alias != alias || record.Msg == nil {
		return "", false
	}
	output, ok := record.Msg.Content.(agent.Output)
	if !ok {
		return "", false
	}
	if record.Msg.Mode != agent.ModeSingle {
		if _, open := r.streaming[alias][record.Msg.StreamID]; open {
			return "", false
		}
	}
	if output.Text == "" {
		return "", false
	}
	return output.Text, true
}

func (r *Room) refreshLatestHandoffSourceLocked(alias participant.Alias) []int {
	prevIdx, hadPrev := r.latestHandoffByAlias[alias]
	if _, active := r.members[alias]; !active {
		return r.clearLatestHandoffSourceLocked(alias, prevIdx, hadPrev)
	}

	source, ok := r.latestHandoffSourceLocked(alias)
	nextIdx := -1
	if ok {
		nextIdx = source.RecordIndex
	}
	return r.swapLatestHandoffSourceLocked(alias, prevIdx, hadPrev, nextIdx, ok)
}

func (r *Room) clearLatestHandoffSourceLocked(alias participant.Alias, prevIdx int, hadPrev bool) []int {
	delete(r.latestHandoffByAlias, alias)
	if !r.hasMarkedHandoffSourceLocked(prevIdx, hadPrev) {
		return nil
	}
	r.records[prevIdx].HandoffSource = false
	return []int{prevIdx}
}

func (r *Room) swapLatestHandoffSourceLocked(alias participant.Alias, prevIdx int, hadPrev bool, nextIdx int, hasNext bool) []int {
	var dirty []int
	if r.hasMarkedHandoffSourceLocked(prevIdx, hadPrev) && prevIdx != nextIdx {
		r.records[prevIdx].HandoffSource = false
		dirty = append(dirty, prevIdx)
	}

	if !hasNext {
		delete(r.latestHandoffByAlias, alias)
		return dirty
	}

	r.latestHandoffByAlias[alias] = nextIdx
	if nextIdx >= 0 && !r.records[nextIdx].HandoffSource {
		r.records[nextIdx].HandoffSource = true
		dirty = append(dirty, nextIdx)
	}
	return dirty
}

func (r *Room) hasMarkedHandoffSourceLocked(index int, ok bool) bool {
	return ok && index >= 0 && index < len(r.records) && r.records[index].HandoffSource
}
