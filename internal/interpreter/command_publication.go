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
		for _, alias := range stagingRouting(staging) {
			event.Routing = append(event.Routing, alias.Name)
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
func (i *Interpreter) publishCommandCompletion(pending pendingInvocation, result runtime.CommandResult, events ...Event) {
	submission := pending.submission
	_, definition := submission.Statement.Value.(promptlang.UserDefinition)
	if isDeliveryStatement(submission.Statement) || definition {
		if pending.submissionPending {
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
