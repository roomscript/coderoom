package session_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/roomscript/coderoom/internal/agent"
	roomconfig "github.com/roomscript/coderoom/internal/config"
	"github.com/roomscript/coderoom/internal/participant"
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

func TestDependencyChecks_inviteWaitsForItsBackend(t *testing.T) {
	errMissing := errors.New("claude --version runs: exit status 1")
	release := make(chan struct{})
	claudeRelease := make(chan struct{})
	codexCancelled := make(chan struct{})
	claudeCancelled := make(chan struct{})
	obs := newTestObserver()
	s := newSession(t,
		session.WithObserver(obs),
		session.WithConfig(loadSessionConfig(t, t.TempDir())),
		fixedFactory(newMockAgent()),
	)
	v := &fakeVerifier{checks: map[string]func(context.Context) error{
		"codex":  blockingCheck(release, codexCancelled, nil),
		"claude": blockingCheck(claudeRelease, claudeCancelled, errMissing),
	}}
	s.StartDependencyChecks(v, []string{"claude", "codex"})

	invite(t, s, "ada") // default backend: codex
	alias := resolvedAlias(s, "ada")
	if _, err := s.Execute(session.RemoveCommand{Alias: alias}); err == nil {
		t.Fatal("expected removal to be rejected while waiting for the check")
	}
	if p, ok := s.Participant(alias); !ok || p.Status != participant.StatusStarting {
		t.Fatalf("waiting participant after rejected removal = %+v, found %v", p, ok)
	}
	select {
	case <-codexCancelled:
		t.Fatal("invite cancelled the check")
	default:
	}
	close(release)
	mustReceive[session.AgentReady](t, obs.ch)
	close(claudeRelease)
	mustReceive[session.BackendUnavailable](t, obs.ch)
	_, _ = s.Execute(session.RemoveCommand{Alias: resolvedAlias(s, "ada")})

	if all := unavailableEvents(s, obs); len(all) != 1 || all[0].Backend != "claude" {
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

func TestDependencyChecks_sharedInviteResult(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "failure"}[fail], func(t *testing.T) {
			release := make(chan struct{})
			started := make(chan struct{})
			var checkErr error
			if fail {
				checkErr = errors.New("install failed")
			}
			obs := newTestObserver()
			s := newSession(t, session.WithObserver(obs),
				session.WithConfig(loadSessionConfig(t, t.TempDir())),
				session.WithAgentFactory(func(*session.Session, roomconfig.ParticipantConfig) (agent.Agent, error) {
					return newMockAgent(), nil
				}),
			)
			s.StartDependencyChecks(&fakeVerifier{checks: map[string]func(context.Context) error{
				"codex": func(context.Context) error { close(started); <-release; return checkErr },
			}}, []string{"codex"})
			waitClosed(t, started, "check start")
			invite(t, s, "ada")
			invite(t, s, "bob")
			for _, name := range []string{"ada", "bob"} {
				p, _ := s.ParticipantByName(name)
				if p.Status != participant.StatusStarting {
					t.Fatalf("participant started before check finished: %+v", p)
				}
			}
			close(release)
			for range 2 {
				if fail {
					mustReceive[session.AgentCrashed](t, obs.ch)
				} else {
					mustReceive[session.AgentReady](t, obs.ch)
				}
			}
			if got := unavailableEvents(s, obs); len(got) != 0 {
				t.Fatalf("duplicate warnings: %+v", got)
			}
		})
	}
}

func TestDependencyChecks_shutdownWaitsForActiveCommand(t *testing.T) {
	started := make(chan struct{})
	cancelled := make(chan struct{})
	release := make(chan struct{})
	obs := newTestObserver()
	s := newSession(t, session.WithObserver(obs))
	s.StartDependencyChecks(&fakeVerifier{checks: map[string]func(context.Context) error{
		"codex": func(ctx context.Context) error {
			close(started)
			<-ctx.Done()
			close(cancelled)
			<-release
			return errors.New("command failed after cancellation")
		},
	}}, []string{"codex"})
	waitClosed(t, started, "command start")
	done := make(chan struct{})
	go func() { s.Shutdown(); close(done) }()
	waitClosed(t, cancelled, "check cancellation")
	select {
	case <-done:
		t.Fatal("shutdown returned before active command finished")
	default:
	}
	close(release)
	waitClosed(t, done, "shutdown")
	if got := unavailableEvents(s, obs); len(got) != 0 {
		t.Fatalf("cancelled check reported: %+v", got)
	}
}
