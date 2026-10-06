package interpreter

import (
	"sync"

	"github.com/roomscript/coderoom/internal/session"
)

// sessionEventInbox owns cross-goroutine session-event buffering and coalesces
// wake-ups for the serialized interpreter executor.
type sessionEventInbox struct {
	mu           sync.Mutex
	events       []session.Event
	drainPending bool
}

// Record appends an event and reports whether the executor needs a drain
// operation. At most one drain operation is pending at a time.
func (i *sessionEventInbox) Record(event session.Event) bool {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.events = append(i.events, event)
	if i.drainPending {
		return false
	}
	i.drainPending = true
	return true
}

// Take removes and returns the currently buffered events. It never changes the
// pending drain marker.
func (i *sessionEventInbox) Take() []session.Event {
	i.mu.Lock()
	defer i.mu.Unlock()
	events := i.events
	i.events = nil
	return events
}

// CompleteDrain atomically clears the pending drain marker if the inbox is
// empty. It reports whether the drain cycle is complete. If an event arrived
// since the last Take, the marker remains set and the caller must keep draining.
func (i *sessionEventInbox) CompleteDrain() bool {
	i.mu.Lock()
	defer i.mu.Unlock()
	if len(i.events) != 0 {
		return false
	}
	i.drainPending = false
	return true
}
