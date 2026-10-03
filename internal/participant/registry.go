package participant

import "fmt"

// Registry tracks all participants in a session by alias.
// It is not safe for concurrent use; the caller is responsible for synchronisation.
type Registry struct {
	participants map[string]*Participant
	colors       colorAllocator
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry {
	return &Registry{participants: make(map[string]*Participant)}
}

// Add registers a participant. Returns an error if p is nil, the alias is
// empty, or the alias is already taken.
func (r *Registry) Add(p *Participant) error {
	if p == nil {
		return fmt.Errorf("participant must not be nil")
	}
	if p.Alias == "" {
		return fmt.Errorf("participant alias must not be empty")
	}
	if _, exists := r.participants[p.Alias]; exists {
		return fmt.Errorf("participant %q already registered", p.Alias)
	}
	p.Color = r.colors.next()
	r.participants[p.Alias] = p
	return nil
}

// Get returns the participant with the given alias.
func (r *Registry) Get(alias string) (*Participant, bool) {
	p, ok := r.participants[alias]
	return p, ok
}

// Remove removes the participant with the given alias.
// Returns an error if the alias is not found.
func (r *Registry) Remove(alias string) error {
	if _, exists := r.participants[alias]; !exists {
		return fmt.Errorf("participant %q not found", alias)
	}
	delete(r.participants, alias)
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
