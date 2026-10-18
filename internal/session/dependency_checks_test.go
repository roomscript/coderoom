package session_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/roomscript/coderoom/internal/policy"
	"github.com/roomscript/coderoom/internal/session"
)

// fakeVerifier runs a per-backend check function and records which backends
// were checked.
type fakeVerifier struct {
	mu      sync.Mutex
	checks  map[string]func(ctx context.Context) error
	checked []string
}

func (v *fakeVerifier) VerifyDependencies(ctx context.Context, backend string) error {
	v.mu.Lock()
	v.checked = append(v.checked, backend)
	check := v.checks[backend]
	v.mu.Unlock()
	if check == nil {
		return nil
	}
	return check(ctx)
}

func (v *fakeVerifier) checkedBackends() []string {
	v.mu.Lock()
	defer v.mu.Unlock()
	return append([]string(nil), v.checked...)
}

// blockingCheck blocks until ctx is cancelled or release is closed, then
// returns err. cancelled is closed when ctx is cancelled first.
func blockingCheck(release <-chan struct{}, cancelled chan<- struct{}, err error) func(context.Context) error {
	return func(ctx context.Context) error {
		select {
		case <-ctx.Done():
			close(cancelled)
			return ctx.Err()
		case <-release:
			return err
		}
	}
}

// unavailableEvents shuts s down, which waits for its checks, and returns
// every BackendUnavailable it reported.
func unavailableEvents(s *session.Session, obs *testObserver) []session.BackendUnavailable {
	s.Shutdown()
	obs.mu.Lock()
	defer obs.mu.Unlock()
	var got []session.BackendUnavailable
	for _, e := range obs.events {
		if u, ok := e.(session.BackendUnavailable); ok {
			got = append(got, u)
		}
	}
	return got
}

func waitClosed(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

func TestDependencyChecks_reportFailuresOnly(t *testing.T) {
	errMissing := errors.New("npx is installed: not found in PATH")
	obs := newTestObserver()
	s := newSession(t, session.WithObserver(obs))
	v := &fakeVerifier{checks: map[string]func(context.Context) error{
		"codex": func(context.Context) error { return errMissing },
	}}

	s.StartDependencyChecks(v, []string{"claude", "codex"})

	got := mustReceive[session.BackendUnavailable](t, obs.ch)
	if got.Backend != "codex" || !errors.Is(got.Err, errMissing) {
		t.Fatalf("BackendUnavailable = %+v, want codex", got)
	}
	if all := unavailableEvents(s, obs); len(all) != 1 {
		t.Fatalf("BackendUnavailable events = %+v, want only codex", all)
	}
}

func TestDependencyChecks_inviteCancelsOnlyItsBackend(t *testing.T) {
	errMissing := errors.New("claude --version runs: exit status 1")
	release := make(chan struct{})
	codexCancelled := make(chan struct{})
	claudeCancelled := make(chan struct{})
	obs := newTestObserver()
	s := newSession(t,
		session.WithObserver(obs),
		session.WithConfig(loadSessionConfig(t, t.TempDir())),
		fixedFactory(newMockAgent()),
	)
	v := &fakeVerifier{checks: map[string]func(context.Context) error{
		"codex":  blockingCheck(release, codexCancelled, errMissing),
		"claude": blockingCheck(release, claudeCancelled, errMissing),
	}}
	s.StartDependencyChecks(v, []string{"claude", "codex"})

	invite(t, s, "ada") // default backend: codex
	mustReceive[session.AgentReady](t, obs.ch)
	waitClosed(t, codexCancelled, "codex check cancellation")
	close(release)
	if got := mustReceive[session.BackendUnavailable](t, obs.ch); got.Backend != "claude" {
		t.Fatalf("BackendUnavailable = %+v, want claude", got)
	}
	_, _ = s.Execute(session.RemoveCommand{Alias: resolvedAlias(s, "ada")})

	if all := unavailableEvents(s, obs); len(all) != 1 {
		t.Fatalf("BackendUnavailable events = %+v, want only claude", all)
	}
}

func TestDependencyChecks_skippedWhenEchoInvitesOn(t *testing.T) {
	obs := newTestObserver()
	s := newSession(t, session.WithObserver(obs))
	if _, err := s.Execute(session.EnablePolicyCommand{Name: policy.EchoInvites}); err != nil {
		t.Fatalf("EnablePolicyCommand: %v", err)
	}
	v := &fakeVerifier{}

	s.StartDependencyChecks(v, []string{"codex"})

	s.Shutdown()
	if got := v.checkedBackends(); len(got) != 0 {
		t.Fatalf("checked %v, want none", got)
	}
}

func TestDependencyChecks_enablingEchoInvitesCancelsThem(t *testing.T) {
	cancelled := make(chan struct{})
	obs := newTestObserver()
	s := newSession(t, session.WithObserver(obs))
	v := &fakeVerifier{checks: map[string]func(context.Context) error{
		"codex": blockingCheck(nil, cancelled, nil),
	}}
	s.StartDependencyChecks(v, []string{"codex"})

	if _, err := s.Execute(session.EnablePolicyCommand{Name: policy.EchoInvites}); err != nil {
		t.Fatalf("EnablePolicyCommand: %v", err)
	}

	waitClosed(t, cancelled, "codex check cancellation")
	if got := unavailableEvents(s, obs); len(got) != 0 {
		t.Fatalf("BackendUnavailable events = %+v, want none", got)
	}
}

func TestDependencyChecks_shutdownCancelsAndWaits(t *testing.T) {
	cancelled := make(chan struct{})
	obs := newTestObserver()
	s := newSession(t, session.WithObserver(obs))
	v := &fakeVerifier{checks: map[string]func(context.Context) error{
		"codex": blockingCheck(nil, cancelled, nil),
	}}
	s.StartDependencyChecks(v, []string{"codex"})

	if got := unavailableEvents(s, obs); len(got) != 0 {
		t.Fatalf("BackendUnavailable events = %+v, want none", got)
	}
	// Shutdown returned, so the check has already observed cancellation.
	select {
	case <-cancelled:
	default:
		t.Fatal("check still running after Shutdown")
	}
}
