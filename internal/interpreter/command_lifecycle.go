package interpreter

import (
	"fmt"
	"maps"
	"slices"

	"github.com/roomscript/coderoom/internal/interpreter/monolith"
	"github.com/roomscript/coderoom/internal/interpreter/runtime"
	"github.com/roomscript/coderoom/internal/promptlang"
	"github.com/roomscript/coderoom/internal/room"
)

type pendingInvocation struct {
	lifecycle         *runtime.InvocationLifecycle
	submission        *monolith.CommandSubmission
	submissionPending bool
}

// The new interpreter owns this algorithm and all lifecycle state. The monolith
// supplies only admission, capabilities and ordered publication on the shared lane.
func (i *Interpreter) executeCommand(command runtime.Command, statement promptlang.ParsedStatement) {
	submission := i.fallback.AdmitCommand(command.Name(), statement)
	if submission == nil {
		return
	}
	send := isDeliveryStatement(statement)
	if !send {
		i.publishCommandInput(submission, nil)
	}
	invocation, err := command.Prepare(statement, submission.Context)
	if err != nil {
		if invocation != nil {
			invocation.Close()
		}
		i.publishCommandSubmission(submission, fmt.Errorf("prepare command: %w", err))
		return
	}
	if send {
		i.publishCommandInput(submission, invocation.Staging())
	}
	i.startCommand(invocation, submission, statement)
}

func (i *Interpreter) startCommand(invocation runtime.Invocation, submission *monolith.CommandSubmission, statement promptlang.ParsedStatement) {
	i.nextInvocationID++
	id := i.nextInvocationID
	lifecycle := runtime.NewInvocationLifecycle(invocation, submission.Context)
	lifecycle.RequestAdvance = func() {
		_ = i.fallback.Schedule(func() { i.advanceCommand(id) })
	}
	if i.invocations == nil {
		i.invocations = make(map[uint64]pendingInvocation)
	}
	i.invocations[id] = pendingInvocation{lifecycle: lifecycle, submission: submission}
	enteredWaiting, err := lifecycle.Start(func(result runtime.CommandResult) {
		submission.QueueCompletion(func() { i.completeCommand(id, result) })
	})
	if err != nil {
		delete(i.invocations, id)
		lifecycle.Close(err)
		i.publishCommandSubmission(submission, fmt.Errorf("start command lifecycle: %w", err))
		return
	}
	send := isDeliveryStatement(statement)
	pending := i.invocations[id]
	pending.submissionPending = send && !enteredWaiting
	i.invocations[id] = pending
	if enteredWaiting {
		i.retainStage(id, statement, invocation.Staging())
	}
	if !pending.submissionPending {
		i.publishCommandSubmission(submission, nil)
	}
}

func (i *Interpreter) advanceCommand(id uint64) {
	pending, ok := i.invocations[id]
	if !ok {
		return
	}
	pending.lifecycle.Advance()
	i.updateStage(id)
}

func (i *Interpreter) completeCommand(id uint64, result runtime.CommandResult) {
	pending, ok := i.invocations[id]
	if !ok {
		return
	}
	result, accepted := pending.lifecycle.Complete(result)
	if !accepted {
		return
	}
	result, events := i.finishStage(id, result)
	if pending.submissionPending {
		for _, record := range result.Records {
			if record.Kind == room.KindUserInput && len(record.Routing) != 0 {
				events = append(events, StagedInputDispatched{Raw: record.Text, Routing: slices.Clone(record.Routing)})
			}
		}
	}
	delete(i.invocations, id)
	i.publishCommandCompletion(pending, result, events...)
}

func (i *Interpreter) closeInvocations() {
	i.stage = nil
	for _, id := range slices.Sorted(maps.Keys(i.invocations)) {
		pending := i.invocations[id]
		delete(i.invocations, id)
		result, accepted := pending.lifecycle.Close(ErrClosed)
		if accepted {
			i.publishCommandCompletion(pending, result)
		}
	}
}
