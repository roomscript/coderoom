package registry

import (
	"context"
	"errors"
	"testing"

	"github.com/roomscript/coderoom/internal/agent"
)

// fakeAgent is a backend whose name and dependencies are set by the test.
type fakeAgent struct {
	name string
	opts Options
}

func (a *fakeAgent) Name() string                              { return a.name }
func (a *fakeAgent) Start() error                              { return nil }
func (a *fakeAgent) Send(string) (agent.StreamID, error)       { return "", nil }
func (a *fakeAgent) SendNotice(string) (agent.StreamID, error) { return "", nil }
func (a *fakeAgent) Read() (agent.Message, error)              { return agent.Message{}, errors.New("no messages") }
func (a *fakeAgent) Interrupt() error                          { return nil }
func (a *fakeAgent) Stop() error                               { return nil }

type fakeDependentAgent struct {
	fakeAgent
	deps []agent.Dependency
}

func (a *fakeDependentAgent) ExternalDependencies() []agent.Dependency { return a.deps }

func fakeCtor(name string) Constructor {
	return func(_ string, opts Options) agent.Agent { return &fakeAgent{name: name, opts: opts} }
}

func dependentCtor(name string, deps ...agent.Dependency) Constructor {
	return func(string, Options) agent.Agent {
		return &fakeDependentAgent{fakeAgent: fakeAgent{name: name}, deps: deps}
	}
}

type configuration map[string]string

func (c configuration) ConfiguredBackends() map[string]string { return c }

func mustNew(t *testing.T, ctors ...Constructor) *Registry {
	t.Helper()
	r, err := New(t.TempDir(), ctors...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return r
}

func TestNewRejectsDuplicateNames(t *testing.T) {
	if _, err := New(t.TempDir(), fakeCtor("codex"), fakeCtor("codex")); err == nil {
		t.Fatal("expected error")
	}
}

func TestBuild(t *testing.T) {
	r := mustNew(t, fakeCtor("codex"), fakeCtor("claude"))

	a, err := r.Build("claude", Options{SystemPrompt: "be brief"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	fake := a.(*fakeAgent)
	if fake.name != "claude" || fake.opts.SystemPrompt != "be brief" {
		t.Fatalf("built %+v, want claude with the given options", fake)
	}
	if _, err := r.Build("echo", Options{}); !errors.Is(err, errUnknownBackend) {
		t.Fatalf("Build(echo) error = %v, want unknown backend", err)
	}
}

func TestValidateConfig(t *testing.T) {
	tests := []struct {
		name     string
		cfg      configuration
		wantErrs int
	}{
		{"all known", configuration{"CODEROOM_BACKEND": "claude", ".coderoom/participants/ada.yaml": "codex"}, 0},
		{"no files, unknown default", configuration{"CODEROOM_BACKEND": "cluade"}, 1},
		{"every unknown name reported", configuration{"CODEROOM_BACKEND": "cluade", ".coderoom/participants/ada.yaml": "abc", ".coderoom/participants/tim.yaml": "codex"}, 2},
		{"echo is not configurable", configuration{".coderoom/participants/ada.yaml": "echo"}, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := mustNew(t, fakeCtor("codex"), fakeCtor("claude"))

			err := r.ValidateConfig(tt.cfg)
			if tt.wantErrs == 0 {
				if err != nil {
					t.Fatalf("ValidateConfig: %v", err)
				}
				return
			}
			var joined interface{ Unwrap() []error }
			if !errors.As(err, &joined) || len(joined.Unwrap()) != tt.wantErrs {
				t.Fatalf("ValidateConfig error = %v, want %d errors", err, tt.wantErrs)
			}
			if !errors.Is(err, errUnknownBackend) {
				t.Fatalf("ValidateConfig error = %v, want unknown backend", err)
			}
		})
	}
}

func TestValidateConfigSortsBySource(t *testing.T) {
	r := mustNew(t, fakeCtor("codex"))
	err := r.ValidateConfig(configuration{"b": "x", "a": "y", "c": "z"})

	var joined interface{ Unwrap() []error }
	if !errors.As(err, &joined) {
		t.Fatalf("expected joined errors, got %v", err)
	}
	var sources []string
	for _, e := range joined.Unwrap() {
		sources = append(sources, e.Error()[:1])
	}
	if got := sources; len(got) != 3 || got[0] != "a" || got[1] != "b" || got[2] != "c" {
		t.Fatalf("sources = %v, want [a b c]", got)
	}
}

func TestVerifyDependencies(t *testing.T) {
	errMissing := errors.New("missing")
	var ran []string
	check := func(name string, err error) agent.Dependency {
		return agent.Dependency{Description: name, Verify: func(context.Context) error {
			ran = append(ran, name)
			return err
		}}
	}
	tests := []struct {
		name    string
		ctor    Constructor
		wantErr error
		wantRan []string
	}{
		{"no external dependencies", fakeCtor("codex"), nil, nil},
		{"all pass", dependentCtor("codex", check("first", nil), check("second", nil)), nil, []string{"first", "second"}},
		{"stops at first failure", dependentCtor("codex", check("first", errMissing), check("second", nil)), errMissing, []string{"first"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ran = nil
			r := mustNew(t, tt.ctor)

			err := r.VerifyDependencies(context.Background(), "codex")
			if !errors.Is(err, tt.wantErr) || (tt.wantErr == nil && err != nil) {
				t.Fatalf("VerifyDependencies error = %v, want %v", err, tt.wantErr)
			}
			if len(ran) != len(tt.wantRan) {
				t.Fatalf("ran %v, want %v", ran, tt.wantRan)
			}
		})
	}
}

func TestVerifyDependenciesUnknownBackend(t *testing.T) {
	r := mustNew(t, fakeCtor("codex"))
	if err := r.VerifyDependencies(context.Background(), "claude"); !errors.Is(err, errUnknownBackend) {
		t.Fatalf("error = %v, want unknown backend", err)
	}
}

func TestNewDefaultRegistersCodexOnly(t *testing.T) {
	r, err := NewDefault(t.TempDir())
	if err != nil {
		t.Fatalf("NewDefault: %v", err)
	}
	if got := r.names(); len(got) != 1 || got[0] != "codex" {
		t.Fatalf("backends = %v, want [codex]", got)
	}
}
