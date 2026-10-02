package interpreter

import (
	"sync"

	"github.com/trigosec/coderoom/internal/queue"
)

type eventDispatchItem interface{ eventDispatchItem() }

type publishedEvent struct{ event Event }
type eventDispatchBarrier struct{ reached chan struct{} }

func (publishedEvent) eventDispatchItem()       {}
func (eventDispatchBarrier) eventDispatchItem() {}

// eventDispatcher delivers published events in order without blocking the
// interpreter operation loop on observers.
type eventDispatcher struct {
	queue *queue.Queue[eventDispatchItem]
	done  chan struct{}

	observers []Observer
	closeOnce sync.Once
}

func newEventDispatcher() *eventDispatcher {
	dispatcher := &eventDispatcher{
		queue: queue.New[eventDispatchItem](),
		done:  make(chan struct{}),
	}
	go dispatcher.run()
	return dispatcher
}

// Publish queues an event for ordered observer delivery.
func (d *eventDispatcher) Publish(event Event) {
	d.queue.Push(publishedEvent{event: event})
}

// addObserver installs an observer before any event is published.
func (d *eventDispatcher) addObserver(observer Observer) {
	if observer == nil {
		return
	}
	d.observers = append(d.observers, observer)
}

// Flush waits until every event queued before the call has been delivered.
func (d *eventDispatcher) Flush() {
	reached := make(chan struct{})
	d.queue.Push(eventDispatchBarrier{reached: reached})
	select {
	case <-reached:
	case <-d.done:
	}
}

// Close flushes queued events and stops the delivery goroutine.
func (d *eventDispatcher) Close() {
	d.closeOnce.Do(func() {
		d.Flush()
		d.queue.Close()
	})
	<-d.done
}

func (d *eventDispatcher) run() {
	defer close(d.done)
	for {
		item, ok := d.queue.Pull()
		if !ok {
			return
		}
		switch item := item.(type) {
		case publishedEvent:
			d.deliver(item.event)
		case eventDispatchBarrier:
			close(item.reached)
		}
	}
}

func (d *eventDispatcher) deliver(event Event) {
	for _, observer := range d.observers {
		observer.OnEvent(event)
	}
}
