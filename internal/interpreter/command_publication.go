package interpreter

import (
	"errors"

	"github.com/roomscript/coderoom/internal/interpreter/monolith"
	"github.com/roomscript/coderoom/internal/interpreter/runtime"
	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/promptlang"
	"github.com/roomscript/coderoom/internal/room"
)

// Input acceptance belongs to the command owner. Sends wait for preparation to
// supply frozen routing; other commands record input before preparation.
func (i *Interpreter) publishCommandInput(submission *monolith.CommandSubmission, staging *runtime.Staging) {
	event := InputAccepted{Raw: submission.Statement.Raw}
	if _, send := submission.Statement.Value.(promptlang.Send); send {
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
	return SubmissionFailed{Raw: submission.Statement.Raw, Operation: submission.Name, Code: code, Err: err}
}

// Direct sends settle submission after dispatch. Queued sends have already
// acknowledged retention, so their later delivery failures are operation failures.
func (i *Interpreter) publishCommandCompletion(pending pendingInvocation, result runtime.CommandResult, events ...Event) {
	submission := pending.submission
	if _, send := submission.Statement.Value.(promptlang.Send); send {
		if pending.submissionPending {
			events = append(events, commandSubmissionEvent(submission, result.Err))
		} else if result.Err != nil {
			events = append(events, OperationFailed{Raw: submission.Statement.Raw, Operation: submission.Name, Err: result.Err})
		}
		result.Err = nil // The supplied events carry the send's terminal outcome.
	}
	submission.PublishCompletion(result, events...)
}
