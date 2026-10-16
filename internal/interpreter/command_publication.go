package interpreter

import (
	"errors"

	"github.com/roomscript/coderoom/internal/interpreter/monolith"
	"github.com/roomscript/coderoom/internal/interpreter/runtime"
	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/promptlang"
	"github.com/roomscript/coderoom/internal/room"
)

// Input acceptance belongs to the command owner. Deliveries wait for preparation to
// supply frozen routing; other commands record input before preparation.
func (i *Interpreter) publishCommandInput(submission *monolith.CommandSubmission, staging *runtime.Staging) {
	event := InputAccepted{Raw: submission.Statement.Raw}
	if isDeliveryStatement(submission.Statement) {
		for _, aliases := range [][]participant.Alias{staging.WaitFor, staging.ToInform} {
			for _, alias := range aliases {
				event.Routing = append(event.Routing, alias.Name)
			}
		}
	} else {
		i.fallback.AppendRecord(room.Record{Kind: room.KindUserInput, Text: event.Raw})
	}
	submission.Publish(event)
}

func (i *Interpreter) publishCommandSubmission(submission *monolith.CommandSubmission, err error) {
	if err != nil {
		submission.Close()
		i.fallback.PublishState()
	}
	submission.Publish(commandSubmissionEvent(submission, err))
}

func commandSubmissionEvent(submission *monolith.CommandSubmission, err error) Event {
	if err == nil {
		return SubmissionSucceeded{Raw: submission.Statement.Raw}
	}
	code := ErrorExecutionFailed
	var unavailable *runtime.RequiredParticipantUnavailableError
	if errors.As(err, &unavailable) {
		code = ErrorParticipantUnavailable
	}
	return SubmissionFailed{Raw: submission.Statement.Raw, Operation: commandOperation(submission), Code: code, Err: err}
}

// Direct deliveries settle submission after dispatch. Queued deliveries have already
// acknowledged retention, so their later delivery failures are operation failures.
func (i *Interpreter) publishCommandCompletion(pending pendingInvocation, result runtime.CommandResult, events ...Event) {
	submission := pending.submission
	if isDeliveryStatement(submission.Statement) {
		if pending.submissionPending {
			events = append(events, commandSubmissionEvent(submission, result.Err))
		} else if result.Err != nil {
			events = append(events, OperationFailed{Raw: submission.Statement.Raw, Operation: commandOperation(submission), Err: result.Err})
		}
		result.Err = nil // The supplied events carry the delivery's terminal outcome.
	}
	submission.PublishCompletion(result, events...)
}

// Delivery statements publish frozen acceptance routing and settle direct input
// only after the native delivery result.
func isDeliveryStatement(statement promptlang.ParsedStatement) bool {
	switch statement.Value.(type) {
	case promptlang.Send, promptlang.Broadcast:
		return true
	default:
		return false
	}
}

func commandOperation(submission *monolith.CommandSubmission) string {
	if _, broadcast := submission.Statement.Value.(promptlang.Broadcast); broadcast {
		return "staged dispatch"
	}
	return submission.Name
}
