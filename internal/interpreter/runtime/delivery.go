package runtime

import (
	"errors"
	"fmt"
	"sync"

	"github.com/roomscript/coderoom/internal/session"
)

type deliveryState uint8

const (
	deliveryPrepared deliveryState = iota
	deliveryAwaitingOutcome
	deliveryClosed
)

// ErrInvalidDeliveryOutcome reports a terminal outcome that violates the
// execution protocol or the command's delivery semantics.
var ErrInvalidDeliveryOutcome = errors.New("invalid delivery outcome")

// Delivery supplies reusable subscription, launch, single-terminal-event and close
// mechanics. Its zero value is prepared. It owns no invocation identity,
// stage, readiness advancement or publication. The interpreter drives those via
// InvocationLifecycle. Commands own construction and terminal-result formatting.
// Preparation, Go and UpdatePrepared run on the interpreter's lane; callbacks
// and Close may race.
type Delivery struct {
	mu          sync.Mutex
	state       deliveryState
	unsubscribe func()
}

// UpdatePrepared applies command-owned plan changes only before launch/close.
// The update must not call back into Delivery.
func (d *Delivery) UpdatePrepared(update func()) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.state == deliveryPrepared {
		update()
	}
}

// Go subscribes before admitting one command. Terminal events may race launch;
// InvocationLifecycle rejects results from failed starts and owns final settlement.
func (d *Delivery) Go(execution SessionExecution, command session.Command, complete func(session.RoutingResult, error)) error {
	if err := d.subscribe(execution, complete); err != nil {
		return err
	}
	if err := execution.Execute(command); err != nil {
		d.Close()
		return fmt.Errorf("launch delivery: %w", err)
	}
	return nil
}

func (d *Delivery) subscribe(execution SessionExecution, complete func(session.RoutingResult, error)) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.state != deliveryPrepared {
		return ErrInvocationNotPrepared
	}
	d.state = deliveryAwaitingOutcome
	d.unsubscribe = execution.Subscribe(func(event ExecutionEvent) {
		result, err := executionOutcome(event)
		unsubscribe := d.acceptOutcome()
		if unsubscribe == nil {
			return
		}
		unsubscribe()
		complete(result, err)
	})
	return nil
}

func executionOutcome(event ExecutionEvent) (session.RoutingResult, error) {
	switch event := event.(type) {
	case DeliveryCompleted:
		return event.Result, nil
	case ExecutionFailed:
		if event.Err != nil {
			return session.RoutingResult{}, event.Err
		}
	}
	return session.RoutingResult{}, ErrInvalidDeliveryOutcome
}

func (d *Delivery) acceptOutcome() func() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.state != deliveryAwaitingOutcome {
		return nil
	}
	d.state = deliveryClosed
	unsubscribe := d.unsubscribe
	d.unsubscribe = nil
	return unsubscribe
}

// Close suppresses future launches/events and releases the terminal subscription.
// The host owns admitted execution; InvocationLifecycle owns final settlement.
func (d *Delivery) Close() {
	d.mu.Lock()
	d.state = deliveryClosed
	unsubscribe := d.unsubscribe
	d.unsubscribe = nil
	d.mu.Unlock()
	if unsubscribe != nil {
		unsubscribe()
	}
}
