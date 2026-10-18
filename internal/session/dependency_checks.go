package session

import (
	"context"
	"sync"

	"github.com/roomscript/coderoom/internal/policy"
)

// DependencyVerifier runs a backend's external dependency checks.
type DependencyVerifier interface {
	VerifyDependencies(ctx context.Context, backend string) error
}

// dependencyChecks tracks the running checks. Reporting is best effort: a
// result is dropped if its check was cancelled before it finished, but a
// failure that finishes just as an invite cancels its check may still be
// reported right after the agent starts. mu is never held while observers run.
type dependencyChecks struct {
	mu      sync.Mutex
	running map[string]*dependencyCheck // by backend name
	wg      sync.WaitGroup
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
	s.checks.mu.Lock()
	defer s.checks.mu.Unlock()
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
	defer check.cancel()
	if s.retireDependencyCheck(backend, check) && err != nil && ctx.Err() == nil {
		s.notify(BackendUnavailable{Backend: backend, Err: err})
	}
}

// retireDependencyCheck removes check and reports whether it was still the
// running check for backend, that is, not cancelled.
func (s *Session) retireDependencyCheck(backend string, check *dependencyCheck) bool {
	s.checks.mu.Lock()
	defer s.checks.mu.Unlock()
	if s.checks.running[backend] != check {
		return false
	}
	delete(s.checks.running, backend)
	return true
}

// cancelDependencyCheck stops backend's check, if one is running, and drops
// its result.
func (s *Session) cancelDependencyCheck(backend string) {
	s.checks.mu.Lock()
	defer s.checks.mu.Unlock()
	if check, ok := s.checks.running[backend]; ok {
		delete(s.checks.running, backend)
		check.cancel()
	}
}

// cancelAllDependencyChecks stops every running check and drops its result.
func (s *Session) cancelAllDependencyChecks() {
	s.checks.mu.Lock()
	defer s.checks.mu.Unlock()
	for backend, check := range s.checks.running {
		delete(s.checks.running, backend)
		check.cancel()
	}
}
