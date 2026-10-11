package runtime

import (
	"errors"
	"fmt"
)

var (
	// ErrInvocationNotPrepared rejects repeated launches and launches after close.
	ErrInvocationNotPrepared = errors.New("invocation is no longer prepared")
	// ErrStagingUnsupported prevents the prototype from bypassing readiness waits.
	ErrStagingUnsupported = errors.New("invocation staging is not implemented")
)

type invocationState uint8

const (
	invocationPrepared invocationState = iota
	invocationRunning
	invocationFinished
)

// InvocationLifecycle composes command-specific work with shared lifecycle state.
// The host prepares and retains it, and calls all methods on its serialized path.
// It owns no queue, worker, participant subscription or public event publication.
// This prototype supports only invocations with no staging requirements.
type InvocationLifecycle struct {
	invocation Invocation
	state      invocationState
}

// NewInvocationLifecycle wraps one prepared invocation, which must not be nil.
func NewInvocationLifecycle(invocation Invocation) *InvocationLifecycle {
	return &InvocationLifecycle{invocation: invocation}
}

// Start initiates work once. The callback may run anywhere and must only enqueue;
// it must not call Complete inline. A nil return acknowledges successful launch.
func (l *InvocationLifecycle) Start(enqueueCompletion func(CommandResult)) error {
	if l.state != invocationPrepared {
		return ErrInvocationNotPrepared
	}
	if enqueueCompletion == nil {
		return errors.New("invocation requires a completion queue")
	}
	if l.invocation.Staging() != nil {
		return ErrStagingUnsupported
	}
	l.state = invocationRunning
	if err := l.invocation.Go(enqueueCompletion); err != nil {
		l.state = invocationFinished
		l.invocation.Close()
		return fmt.Errorf("start invocation: %w", err)
	}
	return nil
}

// Complete consumes a queued completion once after successful launch. Its return
// tells the host whether to publish the supplied completion. Duplicate callbacks,
// callbacks from failed launches and callbacks after Close are ignored.
func (l *InvocationLifecycle) Complete(completion CommandResult) (CommandResult, bool) {
	if l.state != invocationRunning {
		return CommandResult{}, false
	}
	l.state = invocationFinished
	l.invocation.Close()
	return completion, true
}

// Close settles unfinished work with the host's reason, which must not be nil.
// The host cancels/joins workers and drains queued completions first. Closing a
// finished invocation produces no second outcome and rejects late completions.
func (l *InvocationLifecycle) Close(reason error) (CommandResult, bool) {
	if l.state == invocationFinished {
		return CommandResult{}, false
	}
	l.state = invocationFinished
	l.invocation.Close()
	return CommandResult{Err: reason}, true
}
