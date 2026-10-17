package interpreter

import (
	"errors"

	"github.com/roomscript/coderoom/internal/interpreter/monolith"
	"github.com/roomscript/coderoom/internal/interpreter/runtime"
	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/promptlang"
	"github.com/roomscript/coderoom/internal/room"
)

// invocationPublication binds one invocation's identity and original source to
// the existing ordered publication path. It holds no independent lifecycle state:
// the interpreter's pending identity and runtime lifecycle determine permission.
// Only the interpreter calls the private acceptance/final-publication methods.
type invocationPublication struct {
	interpreter *Interpreter
	id          uint64
	submission  *monolith.CommandSubmission
}

var _ runtime.InvocationPublication = (*invocationPublication)(nil)

// Publish emits intermediate output on the interpreter lane. Subcommand callbacks
// collect facts; Go or HandlePendingEvents decides what output to publish.
func (p *invocationPublication) Publish(records []room.Record, events ...Event) {
	pending, ok := p.interpreter.invocations[p.id]
	if !ok || !pending.lifecycle.IsRunning() {
		return
	}
	p.submission.PublishRecords(records, events...)
}

// Input acceptance belongs to the command owner. Deliveries wait for preparation to
// supply frozen routing; other commands record input before preparation.
func (p *invocationPublication) publishInput(staging *runtime.Staging) {
	submission := p.submission
	event := InputAccepted{Raw: submission.Statement.Raw}
	if isDeliveryStatement(submission.Statement) {
		for _, alias := range stagingRouting(staging) {
			event.Routing = append(event.Routing, alias.Name)
		}
	} else {
		p.interpreter.fallback.AppendRecord(room.Record{Kind: room.KindUserInput, Text: event.Raw})
	}
	submission.Publish(event)
}

func (p *invocationPublication) publishSubmission(err error) {
	submission := p.submission
	if err != nil {
		submission.Close()
		p.interpreter.fallback.PublishState()
	}
	submission.Publish(commandSubmissionEvent(submission, err))
}

func (p *invocationPublication) rejectUnknown(name string, err error) {
	p.submission.Close()
	p.submission.Publish(UnknownCommand{Raw: p.submission.Statement.Raw, Name: name, Err: err})
}

func commandSubmissionEvent(submission *monolith.CommandSubmission, err error) Event {
	if err == nil {
		return SubmissionSucceeded{Raw: submission.Statement.Raw}
	}
	code := commandErrorCode(err)
	var unavailable *runtime.RequiredParticipantUnavailableError
	if errors.As(err, &unavailable) {
		code = ErrorParticipantUnavailable
	}
	return SubmissionFailed{Raw: submission.Statement.Raw, Operation: commandOperation(submission), Code: code, Err: err}
}

// Definitions settle after their confirmation record; direct deliveries settle
// after dispatch. Queued deliveries already acknowledged retention, so their later
// delivery failures are operation failures.
func (p *invocationPublication) publishFinal(submissionPending bool, result runtime.CommandResult, events ...Event) {
	submission := p.submission
	_, definition := submission.Statement.Value.(promptlang.UserDefinition)
	if isDeliveryStatement(submission.Statement) || definition || submissionPending {
		if submissionPending {
			events = append(events, commandSubmissionEvent(submission, result.Err))
		} else if result.Err != nil {
			events = append(events, OperationFailed{Raw: submission.Statement.Raw, Operation: commandOperation(submission), Err: result.Err})
		}
		result.Err = nil // The supplied events carry the terminal submission outcome.
	}
	submission.PublishCompletion(result, events...)
}

// Delivery statements publish frozen acceptance routing and settle direct input
// only after the native delivery result.
func isDeliveryStatement(statement promptlang.ParsedStatement) bool {
	switch statement.Value.(type) {
	case promptlang.Send, promptlang.Broadcast, promptlang.Handoff:
		return true
	default:
		return false
	}
}

func commandOperation(submission *monolith.CommandSubmission) string {
	switch statement := submission.Statement.Value.(type) {
	case promptlang.Broadcast, promptlang.Handoff:
		return "staged dispatch"
	case promptlang.UserDefinition:
		return "define /" + statement.Name.Value
	case promptlang.UserCommand:
		return "/" + statement.Name.Value
	}
	return submission.Name
}

func stagingRouting(staging *runtime.Staging) []participant.Alias {
	return append(append([]participant.Alias(nil), staging.WaitFor...), staging.ToInform...)
}

func commandErrorCode(err error) ErrorCode {
	var reserved promptlang.ReservedCommandNameError
	if errors.As(err, &reserved) {
		return ErrorReservedCommand
	}
	var exists promptlang.CommandAlreadyDefinedError
	if errors.As(err, &exists) {
		return ErrorCommandExists
	}
	return ErrorExecutionFailed
}
