package room

import "slices"

func (r *Room) bumpVersionLocked(dirtyIdx ...int) Update {
	r.version++
	r.dirty[r.version] = uniqueRecordIndices(dirtyIdx)
	r.pruneDirtyLocked()
	return Update{RoomID: r.id, Version: r.version}
}

func (r *Room) pruneDirtyLocked() {
	if len(r.dirty) <= deltaHistoryLimit {
		return
	}
	cutoff := r.version - deltaHistoryLimit
	for version := range r.dirty {
		if version <= cutoff {
			delete(r.dirty, version)
		}
	}
}

func (r *Room) notify(update Update) {
	if r.observer != nil {
		r.observer.OnRoomUpdate(update)
	}
}

func cloneRecord(record Record) Record {
	cloned := record
	cloned.Routing = slices.Clone(record.Routing)
	cloned.FailedRouting = slices.Clone(record.FailedRouting)
	cloned.UnsentRouting = slices.Clone(record.UnsentRouting)
	if record.Msg != nil {
		msgCopy := *record.Msg
		cloned.Msg = &msgCopy
	}
	return cloned
}

func uniqueRecordIndices(idxs []int) []int {
	if len(idxs) == 0 {
		return nil
	}
	sorted := append([]int(nil), idxs...)
	slices.Sort(sorted)
	out := sorted[:0]
	for _, idx := range sorted {
		if len(out) > 0 && out[len(out)-1] == idx {
			continue
		}
		out = append(out, idx)
	}
	return out
}
