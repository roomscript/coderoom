package participant

import "fmt"

// Registry tracks current participants in a session by name.
// It is not safe for concurrent use; the caller is responsible for synchronisation.
type Registry struct {
	participants map[string]*Participant
	colors       colorAllocator
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry {
	return &Registry{participants: make(map[string]*Participant)}
}

// Add registers an already-identified participant without changing its alias.
// Returns an error if p is nil, its ID is zero, or its name is empty or taken.
func (r *Registry) Add(p *Participant) error {
	if p == nil {
		return fmt.Errorf("participant must not be nil")
	}
	if p.Alias.Name == "" {
		return fmt.Errorf("participant name must not be empty")
	}
	if p.Alias.ID == (ID{}) {
		return fmt.Errorf("participant ID must not be zero")
	}
	if _, exists := r.participants[p.Alias.Name]; exists {
		return fmt.Errorf("participant %q already registered", p.Alias.Name)
	}
	p.Color = r.colors.next()
	r.participants[p.Alias.Name] = p
	return nil
}

// Get returns the current participant with the given name.
func (r *Registry) Get(name string) (*Participant, bool) {
	p, ok := r.participants[name]
	return p, ok
}

// Remove removes the current participant with the given name.
// Returns an error if the name is not found.
func (r *Registry) Remove(name string) error {
	if _, exists := r.participants[name]; !exists {
		return fmt.Errorf("participant %q not found", name)
	}
	delete(r.participants, name)
	return nil
}

// List returns all registered participants in unspecified order.
func (r *Registry) List() []*Participant {
	result := make([]*Participant, 0, len(r.participants))
	for _, p := range r.participants {
		result = append(result, p)
	}
	return result
}

// ListAvailable returns participants that are safe to send messages to.
// This includes only participants whose agent has started and that are not
// currently in StatusStarting, StatusPreparing, StatusKeepalive, StatusAttached,
// or StatusCrashed.
// Order is unspecified.
func (r *Registry) ListAvailable() []*Participant {
	var out []*Participant
	for _, p := range r.participants {
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
func (r *Registry) ListStarting() []*Participant {
	var out []*Participant
	for _, p := range r.participants {
		if p.Status == StatusStarting {
			out = append(out, p)
		}
	}
	return out
}

// ListCrashed returns all participants currently in StatusCrashed.
// Order is unspecified.
func (r *Registry) ListCrashed() []*Participant {
	var out []*Participant
	for _, p := range r.participants {
		if p.Status == StatusCrashed {
			out = append(out, p)
		}
	}
	return out
}

// ListWorking returns all participants currently in StatusWorking.
// Order is unspecified.
func (r *Registry) ListWorking() []*Participant {
	var out []*Participant
	for _, p := range r.participants {
		if p.Status == StatusWorking {
			out = append(out, p)
		}
	}
	return out
}

func (r *Registry) hasStatus(s Status) bool {
	for _, p := range r.participants {
		if p.Status == s {
			return true
		}
	}
	return false
}

// HasStarting reports whether any participant is currently in StatusStarting.
func (r *Registry) HasStarting() bool { return r.hasStatus(StatusStarting) }

// HasAttached reports whether any participant is currently in StatusAttached.
func (r *Registry) HasAttached() bool { return r.hasStatus(StatusAttached) }

// HasCrashed reports whether any participant is currently in StatusCrashed.
func (r *Registry) HasCrashed() bool { return r.hasStatus(StatusCrashed) }

// HasWorking reports whether any participant is currently in StatusWorking.
func (r *Registry) HasWorking() bool { return r.hasStatus(StatusWorking) }

// HasKeepalive reports whether any participant is currently in StatusKeepalive.
func (r *Registry) HasKeepalive() bool { return r.hasStatus(StatusKeepalive) }
