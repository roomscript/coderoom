package interpreter

import (
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/roomscript/coderoom/internal/interpreter/runtime"
	"github.com/roomscript/coderoom/internal/promptlang"
	"github.com/roomscript/coderoom/internal/room"
)

type pendingInvocation struct {
	lifecycle           *runtime.InvocationLifecycle
	publication         *invocationPublication
	results             <-chan runtime.CommandResult
	submissionPending   bool
	submissionMilestone runtime.SubmissionAcknowledgement
}

// The new interpreter owns this algorithm and all lifecycle state. The monolith
// supplies only admission, capabilities and ordered publication on the shared lane.
func (i *Interpreter) executeCommand(command runtime.Command, statement promptlang.ParsedStatement) {
	submission := i.fallback.AdmitCommand(command.Name(), statement)
	i.nextInvocationID++
	id := i.nextInvocationID
	submission.Context.EventProcessing = invocationEventProcessing{interpreter: i, id: id}
	publication := &invocationPublication{interpreter: i, id: id, submission: submission}
	submission.Context.Publication = publication
	send := isDeliveryStatement(statement)
	_, named := statement.Value.(promptlang.UserCommand)
	if !send && !named {
		publication.publishInput(nil)
	}
	if err := i.checkCommandAdmission(statement); err != nil {
		publication.publishSubmission(err)
		return
	}
	invocation, err := command.Prepare(statement, submission.Context)
	if err != nil {
		i.rejectCommandPreparation(publication, invocation, err)
		return
	}
	if send || named {
		publication.publishInput(invocation.Staging())
	}
	i.startCommand(invocation, publication)
}

func (i *Interpreter) startCommand(invocation runtime.Invocation, publication *invocationPublication) {
	id, submission := publication.id, publication.submission
	statement := submission.Statement
	lifecycle := runtime.NewInvocationLifecycle(invocation, submission.Context)
	if i.invocations == nil {
		i.invocations = make(map[uint64]pendingInvocation)
	}
	results := make(chan runtime.CommandResult, 1)
	i.invocations[id] = pendingInvocation{lifecycle: lifecycle, publication: publication, results: results}
	enteredWaiting, err := lifecycle.Start(func(result runtime.CommandResult) {
		select {
		case results <- result:
			submission.QueueCompletion(func() { i.completeQueuedCommand(id, results) })
		default: // A duplicate callback cannot block the producer.
		}
	})
	if err != nil {
		delete(i.invocations, id)
		lifecycle.Close(err)
		publication.publishSubmission(fmt.Errorf("start command lifecycle: %w", err))
		return
	}
	send := isDeliveryStatement(statement)
	pending := i.invocations[id]
	_, definition := statement.Value.(promptlang.UserDefinition)
	pending.submissionMilestone, _ = invocation.(runtime.SubmissionAcknowledgement)
	pending.submissionPending = definition || (send && !enteredWaiting) || pending.submissionMilestone != nil
	i.invocations[id] = pending
	if enteredWaiting {
		i.retainStage(id, statement, invocation.Staging())
	}
	if !pending.submissionPending {
		publication.publishSubmission(nil)
	}
	i.acknowledgeCommandMilestone(id)
	// Settle already-queued synchronous results before another input is admitted.
	// Start and acknowledgement still precede settlement; callbacks never settle work.
	i.completeQueuedCommand(id, results)
}

func (i *Interpreter) completeQueuedCommand(id uint64, results <-chan runtime.CommandResult) {
	select {
	case result := <-results:
		i.completeCommand(id, result)
	default:
	}
}

func (i *Interpreter) processCommandEvents(id uint64) {
	pending, ok := i.invocations[id]
	if !ok {
		return
	}
	pending.lifecycle.ProcessEvents()
	i.acknowledgeCommandMilestone(id)
	// Event handlers can complete synchronously, just like Go. Consume their
	// queued result before admitting input triggered by their published output.
	i.completeQueuedCommand(id, pending.results)
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
	pending.publication.publishFinal(pending.submissionPending, result, events...)
}

func (i *Interpreter) closeInvocations() {
	i.stage = nil
	for _, id := range slices.Sorted(maps.Keys(i.invocations)) {
		pending := i.invocations[id]
		delete(i.invocations, id)
		result, accepted := pending.lifecycle.Close(ErrClosed)
		if accepted {
			pending.publication.publishFinal(pending.submissionPending, result)
		}
	}
}

// Room completion updates queue readiness evaluation after the room update burst.
func (i *Interpreter) processCompletedExecutions() {
	for id := range i.invocations {
		_ = i.fallback.Schedule(func() { i.processCommandEvents(id) })
	}
}

// invocationEventProcessing exposes an attention request without a general scheduler.
type invocationEventProcessing struct {
	interpreter *Interpreter
	id          uint64
}

func (r invocationEventProcessing) RequestEventProcessing() {
	_ = r.interpreter.fallback.Schedule(func() { r.interpreter.processCommandEvents(r.id) })
}

// Loop admission is interpreter policy, independent of condition resolution and
// the workflow inside std. The pending map already owns invocation lifetime.
func (i *Interpreter) checkCommandAdmission(statement promptlang.ParsedStatement) error {
	if _, loop := statement.Value.(promptlang.Loop); !loop {
		return nil
	}
	for _, pending := range i.invocations {
		if _, loop := pending.publication.submission.Statement.Value.(promptlang.Loop); loop {
			return errors.New("a loop is already active")
		}
	}
	return nil
}

func (i *Interpreter) acknowledgeCommandMilestone(id uint64) {
	pending, ok := i.invocations[id]
	if !ok || !pending.submissionPending || pending.submissionMilestone == nil {
		return
	}
	if !pending.lifecycle.IsRunning() || !pending.submissionMilestone.ReadyToAcknowledge() {
		return
	}
	pending.submissionPending = false
	i.invocations[id] = pending
	pending.publication.publishSubmission(nil)
}

func (i *Interpreter) rejectCommandPreparation(publication *invocationPublication, invocation runtime.Invocation, err error) {
	if invocation != nil {
		invocation.Close()
	}
	statement := publication.submission.Statement
	named, isNamed := statement.Value.(promptlang.UserCommand)
	var undefined promptlang.UndefinedCommandError
	if isNamed && errors.As(err, &undefined) {
		publication.rejectUnknown(named.Name.Value, err)
		return
	}
	publication.publishSubmission(fmt.Errorf("prepare command: %w", err))
}
