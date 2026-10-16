package claude

import (
	"sync"

	"github.com/roomscript/coderoom/internal/agent"
)

// messageQueue is an unbounded FIFO between the reader goroutines and
// Read(). Unbounded so that reading Claude's stdout never blocks on a slow
// consumer: a blocked stdout reader could not answer permission requests.
type messageQueue struct {
	mu     sync.Mutex
	items  []agent.Message
	closed bool
	ready  chan struct{} // signalled (capacity 1) on push and close
}

func newMessageQueue() *messageQueue {
	return &messageQueue{ready: make(chan struct{}, 1)}
}

func (q *messageQueue) push(msgs ...agent.Message) {
	if len(msgs) == 0 {
		return
	}
	q.mu.Lock()
	if !q.closed {
		q.items = append(q.items, msgs...)
	}
	q.mu.Unlock()
	q.signal()
}

// close marks the end of input. Queued messages remain readable.
func (q *messageQueue) close() {
	q.mu.Lock()
	q.closed = true
	q.mu.Unlock()
	q.signal()
}

// pop blocks until a message is available. ok is false once the queue is
// closed and drained.
func (q *messageQueue) pop() (msg agent.Message, ok bool) {
	for {
		q.mu.Lock()
		if len(q.items) > 0 {
			msg = q.items[0]
			q.items = q.items[1:]
			q.mu.Unlock()
			return msg, true
		}
		closed := q.closed
		q.mu.Unlock()
		if closed {
			return agent.Message{}, false
		}
		<-q.ready
	}
}

func (q *messageQueue) signal() {
	select {
	case q.ready <- struct{}{}:
	default:
	}
}
