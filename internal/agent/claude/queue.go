package claude

import "sync"

// queue is an unbounded FIFO between goroutines. Unbounded so that reading
// Claude's stdout never blocks on a slow consumer: a blocked stdout reader
// could not see the rest of the turn, and Stop would wait on it. It carries
// messages for Read() and permission requests for the approval worker.
type queue[T any] struct {
	mu     sync.Mutex
	items  []T
	closed bool
	ready  chan struct{} // signalled (capacity 1) on push and close
}

func newQueue[T any]() *queue[T] {
	return &queue[T]{ready: make(chan struct{}, 1)}
}

func (q *queue[T]) push(items ...T) {
	if len(items) == 0 {
		return
	}
	q.mu.Lock()
	if !q.closed {
		q.items = append(q.items, items...)
	}
	q.mu.Unlock()
	q.signal()
}

// close marks the end of input. Queued items remain readable.
func (q *queue[T]) close() {
	q.mu.Lock()
	q.closed = true
	q.mu.Unlock()
	q.signal()
}

// pop blocks until an item is available. ok is false once the queue is
// closed and drained.
func (q *queue[T]) pop() (item T, ok bool) {
	for {
		q.mu.Lock()
		if len(q.items) > 0 {
			item = q.items[0]
			q.items = q.items[1:]
			q.mu.Unlock()
			return item, true
		}
		closed := q.closed
		q.mu.Unlock()
		if closed {
			return item, false
		}
		<-q.ready
	}
}

func (q *queue[T]) signal() {
	select {
	case q.ready <- struct{}{}:
	default:
	}
}
