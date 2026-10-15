package interpreter

import (
	"fmt"
	"maps"
	"slices"

	"github.com/roomscript/coderoom/internal/interpreter/monolith"
	"github.com/roomscript/coderoom/internal/interpreter/runtime"
	"github.com/roomscript/coderoom/internal/promptlang"
)

type pendingInvocation struct {
	lifecycle  *runtime.InvocationLifecycle
	submission *monolith.CommandSubmission
}

// The new interpreter owns this algorithm and all lifecycle state. The monolith
// supplies only admission, capabilities and ordered publication on the shared lane.
func (i *Interpreter) executeCommand(command runtime.Command, statement promptlang.ParsedStatement) {
	submission := i.fallback.AdmitCommand(command.Name(), statement)
	if submission == nil {
		return
	}
	invocation, err := command.Prepare(statement, submission.Context)
	if err != nil {
		if invocation != nil {
			invocation.Close()
		}
		submission.PublishSubmission(fmt.Errorf("prepare command: %w", err))
		return
	}
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
		submission.PublishSubmission(fmt.Errorf("start command lifecycle: %w", err))
		return
	}
	if enteredWaiting {
		i.retainStage(id, statement, invocation.Staging())
	}
	submission.PublishSubmission(nil)
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
	delete(i.invocations, id)
	pending.submission.PublishCompletion(result, events...)
}

func (i *Interpreter) closeInvocations() {
	i.stage = nil
	for _, id := range slices.Sorted(maps.Keys(i.invocations)) {
		pending := i.invocations[id]
		delete(i.invocations, id)
		result, accepted := pending.lifecycle.Close(ErrClosed)
		if accepted {
			pending.submission.PublishCompletion(result)
		}
	}
}
