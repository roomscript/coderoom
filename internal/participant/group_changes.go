package participant

import "sync"

// ChangeKind identifies a participant lifecycle fact.
type ChangeKind uint8

// Lifecycle facts delivered by Group are derived from captured views.
const (
	StatusChanged ChangeKind = iota
	BecameReady
	Removed
	WorkCompleted
)

// Change contains detached views of one participant instance before and after a
// lifecycle change. Before is zero for a new member; removal has only Before.
// It carries participant facts, not Session events or execution capabilities.
type Change struct {
	Kind   ChangeKind
	Before View
	After  View
}

// Snapshot returns detached observable views in unspecified order. Like other
// Group reads, the owner must hold its state lock when concurrent changes exist.
func (g *Group) Snapshot() []View {
	members := g.List()
	views := make([]View, len(members))
	for index, member := range members {
		views[index] = member.View
	}
	return views
}

// Subscribe captures initial views and installs a subscription in one owner-locked
// operation. Only subsequently captured changes are delivered. Callbacks may race
// the return once the owner unlocks, but this method never invokes them inline.
func (g *Group) Subscribe(handle func(Change)) ([]View, *Subscription) {
	initial := g.Snapshot()
	if g.subscriptionsClosed {
		return initial, nil
	}
	if g.subscriptions == nil {
		g.subscriptions = make(map[*Subscription]struct{})
	}
	subscription := &Subscription{handle: handle}
	g.subscriptions[subscription] = struct{}{}
	return initial, subscription
}

// Unsubscribe drops queued changes without waiting for an in-flight callback.
// The owner must hold its state lock; it can also call this from a callback after
// reacquiring that lock. Nil and repeated unsubscribe are safe.
func (g *Group) Unsubscribe(subscription *Subscription) {
	if subscription == nil {
		return
	}
	delete(g.subscriptions, subscription)
	subscription.close()
}

// CaptureChanges compares the earlier snapshot with current membership and queues
// participant changes under the owner's state lock. Deliver the result only after
// unlocking, so callbacks can read state or unsubscribe without deadlocking.
func (g *Group) CaptureChanges(before []View) Delivery {
	var delivery Delivery
	if len(g.subscriptions) == 0 {
		return delivery
	}
	changes := deriveGroupChanges(before, g.Snapshot())
	if len(changes) == 0 {
		return delivery
	}
	for subscription := range g.subscriptions {
		subscription.enqueue(changes)
		delivery.subscriptions = append(delivery.subscriptions, subscription)
	}
	return delivery
}

// CloseSubscriptions releases current subscriptions and prevents new ones. It
// leaves membership available for inspection and final lifecycle cleanup.
// The owner must hold its state lock.
func (g *Group) CloseSubscriptions() {
	g.subscriptionsClosed = true
	for subscription := range g.subscriptions {
		g.Unsubscribe(subscription)
	}
}

func deriveGroupChanges(before, after []View) []Change {
	previous := make(map[Alias]View, len(before))
	for _, view := range before {
		previous[view.Alias] = view
	}
	var changes []Change
	for _, view := range after {
		old, existed := previous[view.Alias]
		// Completion precedes idle so readiness cannot bypass pending projection.
		if view.LastCompletedExecutionID > old.LastCompletedExecutionID {
			changes = append(changes, Change{Kind: WorkCompleted, Before: old, After: view})
		}
		if !existed || old.Status != view.Status {
			changes = append(changes, Change{Kind: StatusChanged, Before: old, After: view})
		}
		if !old.StartupReady && view.StartupReady {
			changes = append(changes, Change{Kind: BecameReady, Before: old, After: view})
		}
		delete(previous, view.Alias)
	}
	for _, view := range before {
		if _, removed := previous[view.Alias]; removed {
			changes = append(changes, Change{Kind: Removed, Before: view})
		}
	}
	return changes
}

// Delivery retains exactly the subscribers present at capture. A later subscriber
// gets the resulting snapshot, never changes captured before it joined.
type Delivery struct{ subscriptions []*Subscription }

// Deliver drains captured changes outside the owner's state lock. Concurrent or
// reentrant delivery leaves queued work to each subscription's active delivery loop.
func (d Delivery) Deliver() {
	for _, subscription := range d.subscriptions {
		subscription.deliverQueuedChanges()
	}
}

// Subscription owns its ordered queue. Its mutex protects the queue and delivery
// ownership, never the callback. Group owns registration and unregistration.
type Subscription struct {
	mu         sync.Mutex
	handle     func(Change)
	queue      []Change
	closed     bool
	delivering bool
}

func (s *Subscription) enqueue(changes []Change) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		s.queue = append(s.queue, changes...)
	}
}

func (s *Subscription) deliverQueuedChanges() {
	if !s.beginDelivery() {
		return
	}
	for {
		change, ok := s.takeNextChange()
		if !ok {
			return
		}
		s.handle(change)
	}
}

func (s *Subscription) beginDelivery() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.delivering {
		return false
	}
	s.delivering = true
	return true
}

func (s *Subscription) takeNextChange() (Change, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || len(s.queue) == 0 {
		s.delivering = false
		return Change{}, false
	}
	change := s.queue[0]
	s.queue[0] = Change{}
	s.queue = s.queue[1:]
	return change, true
}

func (s *Subscription) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	s.queue = nil
}
