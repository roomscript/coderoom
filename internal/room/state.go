// Package room owns chat-visible room state projected from session events.
package room

import (
	"sync"

	"github.com/roomscript/coderoom/internal/agent"
	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/queue"
	"github.com/roomscript/coderoom/internal/session"
)

// ID identifies a room.
type ID string

// SharedRoomID is the single shared room's identity for V1.
const SharedRoomID ID = "shared"

// UpdateTriggerKind identifies the applied room input that triggered an update.
type UpdateTriggerKind uint8

const (
	// UpdateTriggerNone identifies updates without workflow-relevant metadata.
	UpdateTriggerNone UpdateTriggerKind = iota
	// UpdateTriggerAgentWorkCompleted identifies the applied stream flush that
	// completed an agent turn.
	UpdateTriggerAgentWorkCompleted
)

// UpdateTrigger carries the narrow causal metadata needed by coordinators that
// must act only after a specific room input has been applied.
type UpdateTrigger struct {
	Kind        UpdateTriggerKind
	Alias       participant.Alias
	StreamID    agent.StreamID
	ExecutionID session.ExecutionID
}

// Update is a lightweight redraw/invalidation signal: it tells a Listener
// that room state changed and should be re-read via Snapshot, without carrying
// the changed data itself.
type Update struct {
	RoomID  ID
	Version uint64
	Trigger UpdateTrigger
}

// Observer is notified when room state changes and should be redrawn.
type Observer interface {
	OnRoomUpdate(Update)
}

// Option configures a Room at construction time.
type Option func(*Room)

// WithObserver registers an Observer to receive room update notifications.
func WithObserver(observer Observer) Option {
	return func(r *Room) {
		r.observer = observer
	}
}

// WithID overrides the room's default ID.
func WithID(id ID) Option {
	return func(r *Room) {
		r.id = id
	}
}

// OpenStreamKind identifies the kind of content an open stream is
// accumulating.
type OpenStreamKind int

// Open stream kind values.
const (
	OpenStreamOutput OpenStreamKind = iota
	OpenStreamReasoning
	OpenStreamCommand
	OpenStreamFileChange
)

// OpenStream describes one record that is still accumulating streamed
// content.
type OpenStream struct {
	Alias     participant.Alias
	RecordIdx int
	StreamID  agent.StreamID
	Kind      OpenStreamKind
}

// Room holds the canonical chat-visible state for one room, projected from
// session events and local, non-session appends.
type Room struct {
	mu                        sync.RWMutex
	id                        ID
	version                   uint64
	lastCompletedExecutionIDs map[participant.Alias]participant.ExecutionID
	members                   map[participant.Alias]struct{}
	departed                  map[participant.Alias]bool
	records                   []Record
	streaming                 map[participant.Alias]map[agent.StreamID]OpenStream
	latestHandoffByAlias      map[participant.Alias]int
	dirty                     map[uint64][]int
	observer                  Observer
	queue                     *queue.Queue[session.Event]
}

// LastCompletedExecutionID returns completed work recorded in room state,
// including completion history established when the interpreter attached.
func (r *Room) LastCompletedExecutionID(alias participant.Alias) participant.ExecutionID {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.lastCompletedExecutionIDs[alias]
}

// InitializeCompletionHistory establishes the attachment boundary without replaying
// historical output. The interpreter calls this before processing Session events.
func (r *Room) InitializeCompletionHistory(views []participant.View) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, view := range views {
		r.recordCompletedExecutionLocked(view.Alias, view.LastCompletedExecutionID)
	}
}

func (r *Room) recordCompletedExecutionLocked(alias participant.Alias, id participant.ExecutionID) {
	if r.lastCompletedExecutionIDs == nil {
		r.lastCompletedExecutionIDs = make(map[participant.Alias]participant.ExecutionID)
	}
	r.lastCompletedExecutionIDs[alias] = max(r.lastCompletedExecutionIDs[alias], id)
}
