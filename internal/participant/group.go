package participant

import "fmt"

// Group owns participant membership, detached snapshots, and ordered change
// subscriptions. The owner serializes Group access with its state lock; participant
// changes and subscription capture must use that same lock. Deliver callbacks only
// after releasing it. Subscriptions synchronize their own delivery queues.
type Group struct {
	participants        map[string]*Participant
	colors              colorAllocator
	subscriptions       map[*Subscription]struct{}
	subscriptionsClosed bool
}

// NewGroup returns an empty Group.
func NewGroup() *Group {
	return &Group{participants: make(map[string]*Participant)}
}

// Add registers an already-identified participant without changing its alias.
// Returns an error if p is nil, its ID is zero, or its name is empty or taken.
func (g *Group) Add(p *Participant) error {
	if p == nil {
		return fmt.Errorf("participant must not be nil")
	}
	if p.Alias.Name == "" {
		return fmt.Errorf("participant name must not be empty")
	}
	if p.Alias.ID == (ID{}) {
		return fmt.Errorf("participant ID must not be zero")
	}
	if _, exists := g.participants[p.Alias.Name]; exists {
		return fmt.Errorf("participant %q already registered", p.Alias.Name)
	}
	p.Color = g.colors.next()
	g.participants[p.Alias.Name] = p
	return nil
}

// Get returns the current participant with the given name.
func (g *Group) Get(name string) (*Participant, bool) {
	p, ok := g.participants[name]
	return p, ok
}

// Remove removes the current participant with the given name.
// Returns an error if the name is not found.
func (g *Group) Remove(name string) error {
	if _, exists := g.participants[name]; !exists {
		return fmt.Errorf("participant %q not found", name)
	}
	delete(g.participants, name)
	return nil
}

// List returns all registered participants in unspecified ordeg.
func (g *Group) List() []*Participant {
	result := make([]*Participant, 0, len(g.participants))
	for _, p := range g.participants {
		result = append(result, p)
	}
	return result
}

// ListAvailable returns participants that are safe to send messages to.
// This includes only participants whose agent has started and that are not
// currently in StatusStarting, StatusPreparing, StatusKeepalive, StatusAttached,
// or StatusCrashed.
// Order is unspecified.
func (g *Group) ListAvailable() []*Participant {
	var out []*Participant
	for _, p := range g.participants {
		if p.Agent == nil {
			continue
		}
		if !p.IsRoutable() {
			continue
		}
		out = append(out, p)
	}
	return out
}

// ListStarting returns all participants currently in StatusStarting.
// Order is unspecified.
func (g *Group) ListStarting() []*Participant {
	var out []*Participant
	for _, p := range g.participants {
		if p.Status == StatusStarting {
			out = append(out, p)
		}
	}
	return out
}

// ListCrashed returns all participants currently in StatusCrashed.
// Order is unspecified.
func (g *Group) ListCrashed() []*Participant {
	var out []*Participant
	for _, p := range g.participants {
		if p.Status == StatusCrashed {
			out = append(out, p)
		}
	}
	return out
}

// ListWorking returns all participants currently in StatusWorking.
// Order is unspecified.
func (g *Group) ListWorking() []*Participant {
	var out []*Participant
	for _, p := range g.participants {
		if p.Status == StatusWorking {
			out = append(out, p)
		}
	}
	return out
}

func (g *Group) hasStatus(s Status) bool {
	for _, p := range g.participants {
		if p.Status == s {
			return true
		}
	}
	return false
}

// HasStarting reports whether any participant is currently in StatusStarting.
func (g *Group) HasStarting() bool { return g.hasStatus(StatusStarting) }

// HasAttached reports whether any participant is currently in StatusAttached.
func (g *Group) HasAttached() bool { return g.hasStatus(StatusAttached) }

// HasCrashed reports whether any participant is currently in StatusCrashed.
func (g *Group) HasCrashed() bool { return g.hasStatus(StatusCrashed) }

// HasWorking reports whether any participant is currently in StatusWorking.
func (g *Group) HasWorking() bool { return g.hasStatus(StatusWorking) }

// HasKeepalive reports whether any participant is currently in StatusKeepalive.
func (g *Group) HasKeepalive() bool { return g.hasStatus(StatusKeepalive) }
