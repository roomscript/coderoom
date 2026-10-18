// Package registry lists the configurable agent backends, builds agents by
// name, and validates configured backend names.
//
// Constructors must have no side effects: the registry builds a bare
// instance of each backend to learn its name and its dependencies.
package registry

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"

	"github.com/roomscript/coderoom/internal/agent"
)

var (
	errDuplicateBackend = errors.New("duplicate backend")
	errUnknownBackend   = errors.New("unknown backend")
)

// Options are the session facilities every backend accepts.
type Options struct {
	Context          context.Context
	ApprovalListener agent.ApprovalListener
	SystemPrompt     string
	Alias            string    // tags --agent-log lines
	Log              io.Writer // --agent-log; nil when off
}

// Constructor builds a backend's agent. It must only set fields: no
// processes, goroutines or I/O.
type Constructor func(cwd string, opts Options) agent.Agent

// Configuration is what ValidateConfig needs from the loaded config.
type Configuration interface {
	ConfiguredBackends() map[string]string // source -> requested name
}

// Registry maps backend names to constructors. It holds no state beyond that
// table and is safe for concurrent use.
type Registry struct {
	cwd   string
	ctors map[string]Constructor
}

// New returns a registry of ctors, keyed by the name each agent reports.
func New(cwd string, ctors ...Constructor) (*Registry, error) {
	r := &Registry{cwd: cwd, ctors: make(map[string]Constructor, len(ctors))}
	for _, ctor := range ctors {
		name := ctor(cwd, Options{}).Name()
		if _, ok := r.ctors[name]; ok {
			return nil, fmt.Errorf("%w: %q", errDuplicateBackend, name)
		}
		r.ctors[name] = ctor
	}
	return r, nil
}

// Build constructs the named backend's agent.
func (r *Registry) Build(name string, opts Options) (agent.Agent, error) {
	ctor, err := r.constructor(name)
	if err != nil {
		return nil, err
	}
	return ctor(r.cwd, opts), nil
}

// ValidateConfig checks that every configured backend name is known. It
// reports every problem, sorted by source.
func (r *Registry) ValidateConfig(cfg Configuration) error {
	backends := cfg.ConfiguredBackends()
	var errs []error
	for _, source := range slices.Sorted(maps.Keys(backends)) {
		if _, err := r.constructor(backends[source]); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", source, err))
		}
	}
	return errors.Join(errs...)
}

// VerifyDependencies runs the named backend's external dependency checks in
// order and returns the first failure, prefixed with the check's
// description. A backend without external dependencies always passes.
func (r *Registry) VerifyDependencies(ctx context.Context, name string) error {
	a, err := r.Build(name, Options{})
	if err != nil {
		return err
	}
	deps, ok := a.(agent.ExternalDependencies)
	if !ok {
		return nil
	}
	for _, dep := range deps.ExternalDependencies() {
		if err := dep.Verify(ctx); err != nil {
			return fmt.Errorf("%s: %w", dep.Description, err)
		}
	}
	return nil
}

func (r *Registry) constructor(name string) (Constructor, error) {
	ctor, ok := r.ctors[name]
	if !ok {
		return nil, fmt.Errorf("%w %q (known: %s)", errUnknownBackend, name, strings.Join(r.names(), ", "))
	}
	return ctor, nil
}

func (r *Registry) names() []string {
	return slices.Sorted(maps.Keys(r.ctors))
}
