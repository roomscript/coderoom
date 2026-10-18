package session

import (
	"context"

	"github.com/roomscript/coderoom/internal/policy"
)

// DependencyVerifier runs a backend's external dependency checks.
type DependencyVerifier interface {
	VerifyDependencies(ctx context.Context, backend string) error
}

// dependencyCheck is one running check. Its pointer identifies it, so a
// result is only reported while its check is still the registered one.
type dependencyCheck struct {
	cancel context.CancelFunc
}

// StartDependencyChecks runs the checks for backends in the background, one
// goroutine per backend, to warn early about missing tools. A failure is
// reported as BackendUnavailable; a pass reports nothing. Checks never block
// an invite: inviting a participant cancels its backend's check. No checks
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
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.checks.running[backend]; ok {
		return
	}
	ctx, cancel := context.WithCancel(s.lifecycle.ctx)
	check := &dependencyCheck{cancel: cancel}
	s.checks.running[backend] = check
	s.checks.wg.Add(1)
	go func() {
		defer s.checks.wg.Done()
		err := v.VerifyDependencies(ctx, backend)
		s.finishDependencyCheck(ctx, backend, check, err)
	}()
}

// finishDependencyCheck reports err unless the check was cancelled first, by
// an invite, the EchoInvites policy, or the session ending.
func (s *Session) finishDependencyCheck(ctx context.Context, backend string, check *dependencyCheck, err error) {
	s.mu.Lock()
	current := s.checks.running[backend] == check
	if current {
		delete(s.checks.running, backend)
	}
	s.mu.Unlock()
	cancelled := ctx.Err() != nil
	check.cancel()
	if current && !cancelled && err != nil {
		s.notify(BackendUnavailable{Backend: backend, Err: err})
	}
}

// cancelDependencyCheck stops backend's check, if one is running, and drops
// its result.
func (s *Session) cancelDependencyCheck(backend string) {
	s.mu.Lock()
	check, ok := s.checks.running[backend]
	delete(s.checks.running, backend)
	s.mu.Unlock()
	if ok {
		check.cancel()
	}
}

// cancelAllDependencyChecks stops every running check and drops its result.
func (s *Session) cancelAllDependencyChecks() {
	s.mu.Lock()
	running := s.checks.running
	s.checks.running = make(map[string]*dependencyCheck)
	s.mu.Unlock()
	for _, check := range running {
		check.cancel()
	}
}
