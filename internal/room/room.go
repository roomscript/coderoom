package room

import (
	"slices"

	"github.com/roomscript/coderoom/internal/agent"
	"github.com/roomscript/coderoom/internal/queue"
	"github.com/roomscript/coderoom/internal/session"
)

// New returns a Room with its background event-processing goroutine
// already running.
func New(opts ...Option) *Room {
	r := &Room{
		id:                 SharedRoomID,
		members:            make(map[string]struct{}),
		departed:           make(map[string]bool),
		streaming:          make(map[agent.StreamID]OpenStream),
		latestHandoffByRef: make(map[string]int),
		dirty:              make(map[uint64][]int),
		queue:              queue.New[session.Event](),
	}
	for _, opt := range opts {
		opt(r)
	}
	go r.run()
	return r
}

// OnEvent implements session.Observer by queuing e for asynchronous
// projection onto the room's background goroutine.
func (r *Room) OnEvent(e session.Event) {
	r.queue.Push(e)
}

// ApplyEvent synchronously projects e onto the room. Application coordinators
// use this when event ordering and the resulting snapshot share one serialized
// execution loop. Callers must not also register this Room as an observer for
// the same event stream.
func (r *Room) ApplyEvent(e session.Event) {
	if update, ok := r.applyEvent(e); ok {
		r.notify(update)
	}
}

// AppendRecord appends a local, non-session record and notifies the
// observer. Use this for content that did not originate from a
// session.Event, such as user input or local system/log notices.
func (r *Room) AppendRecord(record Record) {
	update, ok := r.appendRecord(record)
	if ok {
		r.notify(update)
	}
}

// AppendSystemRecord appends a local system/lifecycle notice.
func (r *Room) AppendSystemRecord(text string) {
	r.AppendRecord(Record{Kind: KindSystem, Text: text})
}

// AppendUserInputRecord appends a local user-authored input record, with
// an optional routing footer.
func (r *Room) AppendUserInputRecord(text string, routing []string) {
	r.AppendRecord(Record{Kind: KindUserInput, Text: text, Routing: slices.Clone(routing)})
}

// AppendLogRecord appends a local diagnostic log line from alias.
func (r *Room) AppendLogRecord(alias, text string) {
	r.AppendRecord(Record{Kind: KindLog, Alias: alias, Text: text})
}

func (r *Room) appendRecord(record Record) (Update, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	idx := len(r.records)
	r.records = append(r.records, cloneRecord(record))
	return r.bumpVersionLocked(idx), true
}

// Close stops the room's background event-processing goroutine. Production
// code does not need to call this — a Room lives for the process lifetime —
// but tests should call it to avoid leaking the goroutine.
func (r *Room) Close() {
	r.queue.Close()
}

func (r *Room) run() {
	for {
		e, ok := r.queue.Pull()
		if !ok {
			return
		}
		if update, ok := r.applyEvent(e); ok {
			r.notify(update)
		}
	}
}
