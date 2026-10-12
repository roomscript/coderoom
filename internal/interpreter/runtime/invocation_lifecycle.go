package runtime

import (
	"errors"
	"fmt"
	"slices"
	"sync"

	"github.com/roomscript/coderoom/internal/participant"
)

// ErrInvocationNotPrepared rejects repeated starts and starts after close.
var ErrInvocationNotPrepared = errors.New("invocation is no longer prepared")

type invocationState uint8

const (
	invocationPrepared invocationState = iota
	invocationWaiting
	invocationRunning
	invocationFailureQueued
	invocationFinished
)

type readinessStatus uint8

const (
	readinessReady readinessStatus = iota
	readinessWaiting
	readinessFailed
)

type readinessDecision struct {
	status        readinessStatus
	err           error
	discardInform []participant.Alias
}

// InvocationLifecycle starts a prepared invocation once and settles its result
// once. Staged work waits in a private participant monitor. The host owns result
// publication and cancellation of admitted delivery. This prototype is unwired.
type InvocationLifecycle struct {
	mu                sync.Mutex
	closeOnce         sync.Once
	ctx               Context
	monitor           *participantMonitor
	invocation        Invocation
	state             invocationState
	enqueueCompletion func(CommandResult)
	discardedInform   map[participant.Alias]bool
	pendingFailure    error
}

// NewInvocationLifecycle wraps one non-nil prepared invocation.
func NewInvocationLifecycle(invocation Invocation, ctx Context) *InvocationLifecycle {
	return &InvocationLifecycle{
		invocation: invocation, ctx: ctx, monitor: newParticipantMonitor(),
	}
}

// Start either starts execution or starts waiting for participants. A nil error
// means acceptance, not necessarily execution. Initial errors return here; errors
// encountered while waiting are queued later. The callback must only enqueue.
func (l *InvocationLifecycle) Start(enqueueCompletion func(CommandResult)) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if err := l.validateStart(enqueueCompletion); err != nil {
		return err
	}
	l.enqueueCompletion = enqueueCompletion

	var err error
	if l.invocation.Staging() == nil {
		err = l.startInvocation()
	} else {
		err = l.startStagedInvocation()
	}
	if err != nil {
		l.state = invocationFinished
		l.releaseResources()
	}
	return err
}

func (l *InvocationLifecycle) validateStart(enqueueCompletion func(CommandResult)) error {
	if l.state != invocationPrepared {
		return ErrInvocationNotPrepared
	}
	if enqueueCompletion == nil {
		return errors.New("invocation requires a completion queue")
	}
	return nil
}

func (l *InvocationLifecycle) startStagedInvocation() error {
	if l.ctx.Participants == nil || l.ctx.Session == nil {
		return errors.New("staging requires participant reads and session events")
	}
	l.state = invocationWaiting
	l.discardedInform = make(map[participant.Alias]bool)
	l.monitor.initialize(l.ctx, l.includesParticipant)

	readiness, err := l.prepareStagedExecution()
	if err != nil {
		return err
	}
	if readiness == readinessWaiting {
		l.monitor.start(l.continueStagedInvocation)
		return nil
	}
	return l.startInvocation()
}

// startInvocation calls Go but never cleans up a failed start. Its caller owns
// that error and cleanup. Go may enqueue a result immediately, so mark running
// before calling it; the host cannot consume that result until this lock is free.
func (l *InvocationLifecycle) startInvocation() error {
	if l.monitor.isStopped() {
		return ErrInvocationNotPrepared
	}
	l.monitor.stop()
	l.state = invocationRunning
	if err := l.invocation.Go(l.enqueueCompletion); err != nil {
		return fmt.Errorf("start invocation: %w", err)
	}
	return nil
}

// Complete accepts one execution result. A failed Go may already have queued a
// callback; the saved failure takes precedence over that callback's result.
func (l *InvocationLifecycle) Complete(result CommandResult) (CommandResult, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()

	switch l.state {
	case invocationRunning:
		l.state = invocationFinished
		l.releaseResources()
		return result, true
	case invocationFailureQueued:
		// Resources were released when this failure was queued.
		l.state = invocationFinished
		return CommandResult{Err: l.pendingFailure}, true
	default:
		return CommandResult{}, false
	}
}

// Close cancels waiting, settles unfinished work, and joins the monitor. Cancel
// before taking the lock so initialization can observe it. Join after unlocking
// so the monitor can finish. Already-admitted delivery remains the host's job.
func (l *InvocationLifecycle) Close(reason error) (CommandResult, bool) {
	l.monitor.cancel()
	l.mu.Lock()
	result, accepted := l.settleForClose(reason)
	l.mu.Unlock()
	l.monitor.wait()
	return result, accepted
}

func (l *InvocationLifecycle) settleForClose(reason error) (CommandResult, bool) {
	if l.state == invocationFinished {
		return CommandResult{}, false
	}
	if l.state == invocationFailureQueued {
		reason = l.pendingFailure
	}
	l.state = invocationFinished
	l.releaseResources()
	return CommandResult{Err: reason}, true
}

// continueStagedInvocation runs on the monitor's routine, not an event producer.
// Return false to keep waiting, true to stop monitoring after execution or error.
func (l *InvocationLifecycle) continueStagedInvocation() (stopMonitoring bool) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.state != invocationWaiting || l.monitor.isStopped() {
		return true
	}
	readiness, err := l.prepareStagedExecution()
	if err != nil {
		l.failMonitoredInvocation(err)
		return true
	}
	if readiness == readinessWaiting {
		return false
	}
	if err := l.startInvocation(); err != nil {
		l.failMonitoredInvocation(err)
	}
	return true
}

// failMonitoredInvocation closes work and queues its failure for host settlement.
func (l *InvocationLifecycle) failMonitoredInvocation(err error) {
	l.state = invocationFailureQueued
	l.pendingFailure = err
	l.releaseResources()
	l.enqueueCompletion(CommandResult{Err: err})
}

// prepareStagedExecution returns ready only after freezing the evaluated facts.
// It may discard departed notices, but never calls Go or changes lifecycle state.
func (l *InvocationLifecycle) prepareStagedExecution() (readinessStatus, error) {
	for {
		if l.monitor.isStopped() {
			return readinessFailed, ErrInvocationNotPrepared
		}
		snapshot := l.monitor.readSnapshot()
		decision := l.evaluateReadiness(snapshot)
		if decision.status == readinessFailed {
			return readinessFailed, decision.err
		}
		l.discardDepartedNotices(decision)

		if decision.status == readinessWaiting {
			return readinessWaiting, nil
		}
		if !l.monitor.commit(snapshot) {
			continue // A collected update changed the facts; evaluate again.
		}
		return readinessReady, nil
	}
}

// evaluateReadiness reads facts and returns a decision without applying effects.
func (l *InvocationLifecycle) evaluateReadiness(snapshot participantSnapshot) readinessDecision {
	staging := l.invocation.Staging()
	decision := readinessDecision{status: readinessReady}
	for _, alias := range staging.WaitFor {
		state, exists := snapshot.states[alias]
		if !exists || state.departed {
			return readinessDecision{
				status: readinessFailed, err: fmt.Errorf("required participant %q is unavailable", alias.Name),
			}
		}
		if !state.isReadyForWork() {
			decision.status = readinessWaiting
		}
	}
	for _, alias := range staging.ToInform {
		if l.discardedInform[alias] {
			continue
		}
		state, exists := snapshot.states[alias]
		if !exists || state.departed {
			decision.discardInform = append(decision.discardInform, alias)
			continue
		}
		if !state.isReadyForWork() {
			decision.status = readinessWaiting
		}
	}
	return decision
}

func (l *InvocationLifecycle) discardDepartedNotices(decision readinessDecision) {
	staging := l.invocation.Staging()
	for _, alias := range decision.discardInform {
		if l.discardedInform[alias] {
			continue
		}
		l.discardedInform[alias] = true
		if staging.DiscardInform != nil {
			staging.DiscardInform(alias)
		}
	}
}

func (l *InvocationLifecycle) includesParticipant(alias participant.Alias) bool {
	staging := l.invocation.Staging()
	return slices.Contains(staging.WaitFor, alias) || slices.Contains(staging.ToInform, alias)
}

// releaseResources stops monitoring and closes invocation resources once.
// Callers set the lifecycle state themselves; cleanup does not change it.
func (l *InvocationLifecycle) releaseResources() {
	l.monitor.stop()
	l.closeOnce.Do(l.invocation.Close)
}
