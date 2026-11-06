package session

import (
	"context"
	"fmt"
	"sync"

	"github.com/roomscript/coderoom/internal/policy"
)

// DependencyVerifier runs a backend's external dependency checks.
type DependencyVerifier interface {
	VerifyDependencies(ctx context.Context, backend string) error
}

// dependencyChecks coordinates shared checks and ownership of their results.
type dependencyChecks struct {
	mu      sync.Mutex
	running map[string]*dependencyCheck // by backend name
	wg      sync.WaitGroup
}

// dependencyCheck is one running check. Its pointer identifies it, so a
// result is only reported while its check is still the registered one.
type dependencyCheck struct {
	cancel  context.CancelFunc
	done    chan struct{}
	err     error
	claimed bool
}

// StartDependencyChecks runs the checks for backends in the background, one
// goroutine per backend, to warn early about missing tools. A failure is
// reported as BackendUnavailable; a pass reports nothing. Checks never block
// an invite command: its start goroutine waits for the backend check. No checks
// run when the EchoInvites policy is on.
func (s *Session) StartDependencyChecks(v DependencyVerifier, backends []string) {
	if s.policies.Enabled(policy.EchoInvites) {
		return
	}
	for _, backend := range backends {
		s.startDependencyCheck(v, backend)
	}
}

func (s *Session) startDependencyCheck(v DependencyVerifier, backend string) {
	s.checks.mu.Lock()
	defer s.checks.mu.Unlock()
	if _, ok := s.checks.running[backend]; ok {
		return
	}
	ctx, cancel := context.WithCancel(s.lifecycle.ctx)
	check := &dependencyCheck{cancel: cancel, done: make(chan struct{})}
	s.checks.running[backend] = check
	s.checks.wg.Add(1)
	go func() {
		defer s.checks.wg.Done()
		err := v.VerifyDependencies(ctx, backend)
		s.finishDependencyCheck(ctx, backend, check, err)
	}()
}

// finishDependencyCheck gives waiting invites the result, or reports it once.
func (s *Session) finishDependencyCheck(ctx context.Context, backend string, check *dependencyCheck, err error) {
	defer check.cancel()
	s.checks.mu.Lock()
	check.err = err
	close(check.done)
	report := s.checks.running[backend] == check && !check.claimed && ctx.Err() == nil
	if s.checks.running[backend] == check {
		delete(s.checks.running, backend)
	}
	s.checks.mu.Unlock()
	if report && err != nil {
		s.notify(BackendUnavailable{Backend: backend, Err: err})
	}
}

// claimDependencyCheck transfers result reporting to all waiting invites.
func (s *Session) claimDependencyCheck(backend string) *dependencyCheck {
	s.checks.mu.Lock()
	defer s.checks.mu.Unlock()
	check := s.checks.running[backend]
	if check != nil {
		check.claimed = true
	}
	return check
}

func (check *dependencyCheck) wait(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return fmt.Errorf("wait for dependency check: %w", ctx.Err())
	case <-check.done:
		return check.err
	}
}

// cancelAllDependencyChecks suppresses results and skips remaining dependencies.
// An external command already running is allowed to finish.
func (s *Session) cancelAllDependencyChecks() {
	s.checks.mu.Lock()
	defer s.checks.mu.Unlock()
	for backend, check := range s.checks.running {
		delete(s.checks.running, backend)
		check.cancel()
	}
}
