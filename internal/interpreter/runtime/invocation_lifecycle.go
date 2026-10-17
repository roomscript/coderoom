package runtime

import (
	"errors"
	"fmt"
	"slices"

	"github.com/roomscript/coderoom/internal/participant"
)

// ErrInvocationNotPrepared rejects repeated starts and starts after close.
var ErrInvocationNotPrepared = errors.New("invocation is no longer prepared")

// ErrNoActiveTargets reports a delivery whose frozen optional recipients all departed.
var ErrNoActiveTargets = errors.New("no active targets")

// RequiredParticipantUnavailableError reports a frozen prerequisite that departed
// or was missing when the lifecycle evaluated readiness. It prevents launch.
type RequiredParticipantUnavailableError struct {
	Alias participant.Alias
}

func (e *RequiredParticipantUnavailableError) Error() string {
	return fmt.Sprintf("required participant %q is unavailable", e.Alias.Name)
}

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

// InvocationLifecycle owns readiness rules and invocation transitions. The caller
// serializes all methods on its lane. Participant callbacks only collect facts
// and request event processing; execution callbacks only queue completion.
type InvocationLifecycle struct {
	ctx               Context
	subscription      *participantSubscription
	invocation        Invocation
	state             invocationState
	enqueueCompletion func(CommandResult)
	discardedInform   map[participant.Alias]bool
	pendingFailure    error
}

// NewInvocationLifecycle wraps one non-nil prepared invocation.
func NewInvocationLifecycle(invocation Invocation, ctx Context) *InvocationLifecycle {
	return &InvocationLifecycle{
		invocation: invocation, ctx: ctx, subscription: &participantSubscription{},
	}
}

// Start reads room completion progress through Context without retaining a copy.
// Start either starts execution or starts waiting for participants. A nil error
// means acceptance, not necessarily execution. Initial errors return here; errors
// encountered while waiting are queued later. The returned boolean records whether
// this start entered waiting, so the owner can retain the stage before processing
// queued advancement. The callback must only enqueue.
func (l *InvocationLifecycle) Start(enqueueCompletion func(CommandResult)) (enteredWaiting bool, err error) {
	if err := l.validateStart(enqueueCompletion); err != nil {
		return false, err
	}
	l.enqueueCompletion = enqueueCompletion

	if l.invocation.Staging() == nil {
		err = l.startInvocation()
	} else {
		enteredWaiting, err = l.startStagedInvocation()
	}
	if err != nil {
		l.state = invocationFinished
		l.releaseResources()
	}
	return enteredWaiting, err
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

func (l *InvocationLifecycle) startStagedInvocation() (bool, error) {
	if l.ctx.Session == nil {
		return false, errors.New("staging requires participant lifecycle subscription")
	}
	l.state = invocationWaiting
	l.discardedInform = make(map[participant.Alias]bool)
	l.subscription.initialize(l.ctx, l.includesParticipant)

	readiness, err := l.prepareStagedExecution()
	if err != nil {
		return false, err
	}
	if readiness == readinessWaiting {
		return true, nil
	}
	return false, l.startInvocation()
}

// startInvocation calls Go but never cleans up a failed start. Its caller owns
// that error and cleanup. Go may enqueue a result immediately, so mark running
// before calling it; the owner must enqueue completion rather than settle inline.
func (l *InvocationLifecycle) startInvocation() error {
	l.subscription.stop()
	l.state = invocationRunning
	if err := l.invocation.Go(l.enqueueCompletion); err != nil {
		return fmt.Errorf("start invocation: %w", err)
	}
	return nil
}

// Complete accepts one execution result. A failed Go may already have queued a
// callback; the saved failure takes precedence over that callback's result.
func (l *InvocationLifecycle) Complete(result CommandResult) (CommandResult, bool) {
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

// Close settles unfinished work and releases subscriptions on the owner's lane.
// Already-admitted delivery remains the caller's responsibility.
func (l *InvocationLifecycle) Close(reason error) (CommandResult, bool) {
	if l.state == invocationFinished {
		return CommandResult{}, false
	}
	if l.state == invocationFailureQueued {
		l.state = invocationFinished
		return CommandResult{Err: l.pendingFailure}, true
	}
	l.state = invocationFinished
	l.releaseResources()
	return CommandResult{Err: reason}, true
}

// ProcessEvents evaluates readiness while waiting and delegates collected events
// to an optional InvocationEventHandler while running. The owner serializes this method;
// requests processed after settlement or shutdown do nothing.
func (l *InvocationLifecycle) ProcessEvents() {
	if l.state == invocationRunning {
		if handler, ok := l.invocation.(InvocationEventHandler); ok {
			handler.HandlePendingEvents()
		}
		return
	}
	if l.state != invocationWaiting {
		return
	}
	readiness, err := l.prepareStagedExecution()
	if err != nil {
		l.failInvocation(err)
		return
	}
	if readiness == readinessWaiting {
		return
	}
	if err := l.startInvocation(); err != nil {
		l.failInvocation(err)
	}
}

// failInvocation closes work and queues its failure for owner settlement.
func (l *InvocationLifecycle) failInvocation(err error) {
	l.state = invocationFailureQueued
	l.pendingFailure = err
	l.releaseResources()
	l.enqueueCompletion(CommandResult{Err: err})
}

// prepareStagedExecution returns ready only after freezing the evaluated facts.
// It may discard departed notices, but never calls Go or changes lifecycle state.
func (l *InvocationLifecycle) prepareStagedExecution() (readinessStatus, error) {
	for {
		snapshot := l.subscription.readSnapshot()
		decision := l.evaluateReadiness(snapshot)
		if decision.status == readinessFailed {
			return readinessFailed, decision.err
		}
		l.discardDepartedNotices(decision)

		if decision.status == readinessWaiting {
			return readinessWaiting, nil
		}
		if !l.subscription.commit(snapshot) {
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
				status: readinessFailed, err: &RequiredParticipantUnavailableError{Alias: alias},
			}
		}
		if !l.isReadyForWork(alias, state) {
			decision.status = readinessWaiting
		}
	}
	return l.evaluateOptionalReadiness(snapshot, decision)
}

// Availability and propagated completion are one prerequisite for both required
// and optional participants. Only their departure policies differ.
func (l *InvocationLifecycle) isReadyForWork(alias participant.Alias, state participantState) bool {
	if !state.isReadyForWork() {
		return false
	}
	if state.lastCompletedExecutionID == 0 {
		return true
	}
	return l.ctx.CompletedExecutions != nil && state.lastCompletedExecutionID <= l.ctx.CompletedExecutions.LastCompletedExecutionID(alias)
}

func (l *InvocationLifecycle) evaluateOptionalReadiness(snapshot participantSnapshot, decision readinessDecision) readinessDecision {
	staging := l.invocation.Staging()
	for _, alias := range staging.ToInform {
		if l.discardedInform[alias] {
			continue
		}
		state, exists := snapshot.states[alias]
		if !exists || state.departed {
			decision.discardInform = append(decision.discardInform, alias)
			continue
		}
		if !l.isReadyForWork(alias, state) {
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

// releaseResources stops collection and closes invocation resources.
// Callers set the lifecycle state themselves; cleanup does not change it.
func (l *InvocationLifecycle) releaseResources() {
	l.subscription.stop()
	l.invocation.Close()
}

// Readiness returns detached collected facts and whether the lifecycle is still
// waiting. DiscardWaiting also checks readiness atomically before removal.
// Departed and missing frozen instances appear unavailable.
func (l *InvocationLifecycle) Readiness() ([]participant.View, bool) {
	staging := l.invocation.Staging()
	if staging == nil {
		return nil, false
	}
	snapshot := l.subscription.readSnapshot()
	aliases := append(slices.Clone(staging.WaitFor), staging.ToInform...)
	var views []participant.View
	for _, alias := range aliases {
		state, exists := snapshot.states[alias]
		view := participant.View{Alias: alias, Status: state.status, StartupReady: state.startupReady}
		if !exists || state.departed {
			view.Status, view.StartupReady = participant.StatusCrashed, false
		}
		views = append(views, view)
	}
	return views, l.state == invocationWaiting
}

// DiscardWaiting discards blocked work on the owner's lane. Collected readiness
// or failure defeats removal, even before queued advancement runs. A successful
// discard freezes collection and closes resources without completion.
func (l *InvocationLifecycle) DiscardWaiting() bool {
	for l.state == invocationWaiting {
		snapshot := l.subscription.readSnapshot()
		decision := l.evaluateReadiness(snapshot)
		if decision.status != readinessWaiting {
			return false // Queued advancement owns ready/failure progress.
		}
		l.discardDepartedNotices(decision)
		if !l.subscription.commit(snapshot) {
			continue
		}
		l.state = invocationFinished
		l.releaseResources()
		return true
	}
	return false
}
