package monolith

import (
	"errors"
	"sync"

	"github.com/roomscript/coderoom/internal/interpreter/runtime"
	"github.com/roomscript/coderoom/internal/session"
)

// commandSession is one execution scope created by an invocation's provider.
type commandSession struct {
	provider *commandSessionProvider
	mu       sync.Mutex
	onClose  func()
	handle   func(runtime.ExecutionEvent)
	admitted bool
	closed   bool
}

func (s *commandSession) Subscribe(handle func(runtime.ExecutionEvent)) func() {
	s.mu.Lock()
	if !s.closed {
		s.handle = handle
	}
	s.mu.Unlock()
	return s.close
}

func (s *commandSession) Execute(command session.Command) error {
	if command == nil {
		return errors.New("execution requires a command")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.admitted || s.handle == nil {
		return errors.New("execution adapter is closed, already admitted or has no subscriber")
	}
	s.provider.mu.Lock()
	ownerClosed := s.provider.closed
	s.provider.mu.Unlock()
	if ownerClosed {
		return ErrClosed
	}
	e := s.provider.executor
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
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.handle = nil
	onClose := s.onClose
	s.mu.Unlock()
	if onClose != nil {
		onClose()
	}
	s.provider.mu.Lock()
	delete(s.provider.executions, s)
	s.provider.mu.Unlock()
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
	handle := s.handle
	s.mu.Unlock()
	s.provider.mu.Lock()
	s.provider.dispatching = true
	s.provider.mu.Unlock()
	executionID, err := e.session.Execute(op.command)
	events := e.takeSessionEvents()
	if handle != nil {
		handle(nativeDeliveryEvent(executionID, events, err))
	}
	s.provider.publishDelivery(e, events)
}

// Session execution always completes, with optional actual routing. Malformed
// routing is a protocol failure; the adapter does not infer command semantics.
func nativeDeliveryEvent(executionID session.ExecutionID, events []session.Event, executionErr error) runtime.ExecutionEvent {
	var routing session.RoutingResult
	count := 0
	for _, event := range events {
		if result, ok := event.(session.RoutingCompleted); ok {
			routing = result.Result
			count++
		}
	}
	if count > 1 || (count == 1 && routing.Kind == "") {
		return runtime.ExecutionFailed{Err: errors.Join(runtime.ErrInvalidDeliveryOutcome, executionErr)}
	}
	if count == 1 {
		routing.Err = executionErr
	}
	return runtime.ExecutionCompleted{ExecutionID: executionID, Result: routing, Err: executionErr}
}
