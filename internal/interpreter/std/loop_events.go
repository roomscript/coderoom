package std

import (
	"fmt"
	"sync"

	"github.com/roomscript/coderoom/internal/interpreter/runtime"
	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/session"
	"github.com/roomscript/coderoom/internal/shell"
)

// Only this inbox is shared with callbacks. Phase changes, publication and
// subsequent launches all run in HandlePendingEvents on the interpreter lane.
type loopEvents struct {
	mu      sync.Mutex
	pending []any
	closed  bool
}

type loopConditionResult struct {
	iteration int
	result    shell.Result
}

type loopDeliveryResult struct {
	result runtime.DeliveryCompleted
	err    error
}

func (i *loopInvocation) collect(event any) {
	i.events.mu.Lock()
	if i.events.closed {
		i.events.mu.Unlock()
		return
	}
	i.events.pending = append(i.events.pending, event)
	i.events.mu.Unlock()
	i.ctx.EventProcessing.RequestEventProcessing()
}

func (e *loopEvents) take() []any {
	e.mu.Lock()
	defer e.mu.Unlock()
	pending := e.pending
	e.pending = nil
	return pending
}
func (e *loopEvents) close() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.closed = true
	e.pending = nil
}

func (i *loopInvocation) collectParticipantEvent(event session.Event) {
	switch event := event.(type) {
	case session.ParticipantStatusChanged:
		if event.Alias == i.primary {
			i.collect(event)
		}
	case session.ParticipantWorkCompleted:
		if event.Alias == i.primary {
			i.collect(event)
		}
	case session.AgentStopped:
		if event.Alias == i.primary {
			i.collect(event)
		}
	case session.AgentCrashed:
		if event.Alias == i.primary {
			i.collect(event)
		}
	}
}

// Each callback only collects and requests attention. The interpreter gives
// control back here; this method consumes facts and returns without waiting.
func (i *loopInvocation) HandlePendingEvents() {
	for _, event := range i.events.take() {
		if i.phase == loopFinished {
			break
		}
		switch event := event.(type) {
		case loopDeliveryResult:
			i.acceptDelivery(event)
		case session.ParticipantWorkCompleted:
			i.work[event.ExecutionID] = true
		case session.ParticipantStatusChanged:
			i.acceptStatus(event)
		case session.AgentStopped:
			i.acceptDeparture("stopped")
		case session.AgentCrashed:
			i.acceptDeparture("crashed")
		case loopConditionResult:
			i.acceptConditionResult(event)
		}
	}
	i.evaluateCompletedWork()
}

func (i *loopInvocation) acceptStatus(event session.ParticipantStatusChanged) {
	if event.To == participant.StatusWorking {
		i.activeExecutionID = event.ActiveExecutionID
		return
	}
	if event.To != participant.StatusIdle || i.activeExecutionID == 0 {
		return
	}
	if _, completed := i.work[i.activeExecutionID]; !completed {
		i.work[i.activeExecutionID] = false
	}
	i.activeExecutionID = 0
}

func (i *loopInvocation) acceptDeparture(status string) {
	i.departure = "[loop] stopped: participant @" + i.primary.Name + " " + status
	// Delivery still owns whether the initial submission succeeded. Retain a
	// departure observed during dispatch until its terminal outcome arrives.
	if i.phase != loopDispatching {
		i.finish(i.departure, nil)
	}
}

func (i *loopInvocation) evaluateCompletedWork() {
	if i.phase != loopWaiting {
		return
	}
	normal, ended := i.work[i.executionID]
	if !ended {
		return
	}
	if normal && i.ctx.CompletedExecutions.LastCompletedExecutionID(i.primary) < i.executionID {
		return
	}
	i.phase = loopEvaluating
	iteration := i.turns
	if err := i.ctx.Shell.Go(i.program, func(result shell.Result) { i.collect(loopConditionResult{iteration: iteration, result: result}) }); err != nil {
		i.finish("", fmt.Errorf("launch loop condition: %w", err))
	}
}

func (i *loopInvocation) acceptCondition(result shell.Result) {
	i.publishCondition(result)
	switch result.Status {
	case shell.StatusSuccess:
		i.finish("[loop] condition /"+i.statement.Condition.Value+" succeeded", nil)
	case shell.StatusCancelled:
		i.finish("[loop] condition /"+i.statement.Condition.Value+" cancelled", nil)
	case shell.StatusFailure:
		if i.turns >= i.statement.MaxTurns.Value {
			i.finish(fmt.Sprintf("[loop] reached /max %d; condition /%s still failing", i.turns, i.statement.Condition.Value), nil)
			return
		}
		if err := i.startDelivery(formatLoopPrompt(i.statement, result)); err != nil {
			i.finish("", err)
		}
	default:
		i.finish("", fmt.Errorf("unexpected loop condition status %q", result.Status))
	}
}

func (i *loopInvocation) acceptConditionResult(event loopConditionResult) {
	if i.phase == loopEvaluating && event.iteration == i.turns {
		i.acceptCondition(event.result)
	}
}
