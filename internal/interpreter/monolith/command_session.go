package monolith

import (
	"errors"
	"sync"

	"github.com/roomscript/coderoom/internal/interpreter/runtime"
	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/session"
)

// commandSession admits opaque Session commands on the shared lane and reports
// terminal events only to its owner before causal publication.
type commandSession struct {
	executor       *interpreterExecutor
	invocation     *ownedCommand
	mu             sync.Mutex
	onClose        func()
	handle         func(runtime.ExecutionEvent)
	admitted       bool
	closed         bool
	completionWork func()
	publication    instructionSequence
	recordCount    int
	dispatching    bool
}

func (s *commandSession) ParticipantByName(name string) (participant.View, bool) {
	return s.executor.session.ParticipantByName(name)
}

func (s *commandSession) CreateParticipantSendPlan(alias participant.Alias) session.ParticipantSendPlan {
	return s.executor.session.CreateParticipantSendPlan(alias)
}

func (s *commandSession) Subscribe(handle func(runtime.ExecutionEvent)) func() {
	s.mu.Lock()
	if !s.closed {
		s.handle = handle
	}
	s.mu.Unlock()
	return s.close
}

// Delegate only to the atomic Session boundary;
// controllers without that boundary cannot be used for lifecycle staging.
func (s *commandSession) SubscribeParticipants(handle func(session.Event)) ([]participant.View, func()) {
	source, ok := s.executor.session.(interface {
		SubscribeParticipants(func(session.Event)) ([]participant.View, func())
	})
	if !ok {
		panic("session controller does not support atomic participant subscriptions")
	}
	return source.SubscribeParticipants(handle)
}

func (s *commandSession) Execute(command session.Command) error {
	if command == nil {
		return errors.New("delivery requires a command")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.admitted || s.handle == nil {
		return errors.New("delivery adapter is closed, already admitted or has no subscriber")
	}
	e := s.executor
	e.enqueueMu.Lock()
	defer e.enqueueMu.Unlock()
	if e.closed {
		return ErrClosed
	}
	e.nativeDeliveries = append(e.nativeDeliveries, nativeDeliveryOperation{session: s, command: command})
	e.operations.Push(drainNativeDeliveriesOperation{})
	s.admitted = true
	return nil
}

func (s *commandSession) close() {
	s.mu.Lock()
	s.closed = true
	s.handle = nil
	s.mu.Unlock()
	if s.onClose != nil {
		s.onClose()
	}
}

func (s *commandSession) complete(completion invocationCompletion) {
	s.queueCompletionWork(func() {
		sequence := s.executor.settleInvocation(completion)
		s.publishCompletion(sequence, len(completion.completion.Records))
	})
}

func (s *commandSession) queueCompletionWork(work func()) {
	s.mu.Lock()
	if s.dispatching {
		if s.completionWork == nil {
			s.completionWork = work
		}
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()
	s.executor.enqueueCompletion(scheduledOperation{work: work})
}

func (s *commandSession) publishCompletion(sequence instructionSequence, recordCount int) {
	s.mu.Lock()
	if s.dispatching {
		s.publication, s.recordCount = sequence, recordCount
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()
	s.executor.runner.Run(sequence)
}

// Admitted delivery belongs to the current scheduled turn. Drain it before the
// next user operation can inspect readiness or compete with its native scope.
// The queued drain also wakes the lane for legacy worker admission.
type drainNativeDeliveriesOperation struct{}

func (drainNativeDeliveriesOperation) apply(e *interpreterExecutor) { e.dispatchNativeDeliveries() }

func (e *interpreterExecutor) dispatchNativeDeliveries() {
	e.enqueueMu.Lock()
	deliveries := e.nativeDeliveries
	e.nativeDeliveries = nil
	e.enqueueMu.Unlock()
	for _, delivery := range deliveries {
		delivery.apply(e)
	}
}

type nativeDeliveryOperation struct {
	session *commandSession
	command session.Command
}

func (op nativeDeliveryOperation) apply(e *interpreterExecutor) {
	s := op.session
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.dispatching = true
	handle := s.handle
	s.mu.Unlock()
	err := e.executeCommand(op.command)
	events := e.takeSessionEvents()
	if handle != nil {
		handle(nativeDeliveryEvent(events, err))
	}
	s.publishDelivery(e, events)
}

// Publish caller-owned completion work, input records, causal events, then the
// terminal outcome. Neither this adapter nor the runner owns the caller's workflow.
func (s *commandSession) publishDelivery(e *interpreterExecutor, events []session.Event) {
	s.mu.Lock()
	work := s.completionWork
	s.completionWork = nil
	s.mu.Unlock()
	if work != nil {
		work() // The owning interpreter settles its result before publication.
	}
	s.mu.Lock()
	publication, count := s.publication, s.recordCount
	s.publication = nil
	s.recordCount = 0
	s.dispatching = false
	s.mu.Unlock()
	// Input records precede the events caused by delivery, as in legacy dispatch.
	items := []runnerItem{}
	var settlement instructionSequence

	if publication != nil {
		items = append(items, instructionItems(publication[:count])...)
		settlement = append(settlement, publication[count:]...)
	}
	items = append(items, sessionEventsItem{events: events})
	items = append(items, instructionItems(settlement)...)
	e.runner.runItems(items)
}

// A terminal execution failure belongs to the invocation, not to an invented
// Session routing event. Actual outcomes are forwarded unchanged.
func nativeDeliveryEvent(events []session.Event, executionErr error) runtime.ExecutionEvent {
	var routing session.RoutingResult
	count := 0
	for _, event := range events {
		if result, ok := event.(session.RoutingCompleted); ok {
			routing = result.Result
			count++
		}
	}
	if count == 0 {
		return runtime.ExecutionFailed{Err: errors.Join(errMissingRoutingOutcome, executionErr)}
	}
	if count != 1 || routing.Kind == "" {
		return runtime.ExecutionFailed{Err: errors.Join(runtime.ErrInvalidDeliveryOutcome, executionErr)}
	}
	return runtime.DeliveryCompleted{Result: routing}
}

func (s *commandSession) release() {
	if s.invocation != nil {
		s.invocation.close()
	}
	s.close()
}
