package interpreter

import (
	"errors"

	"github.com/roomscript/coderoom/internal/promptlang"
)

// withSubmissionSource carries the parsed statement across preparation,
// execution and deferred workflows for source-aware result events.
func withSubmissionSource(sequence instructionSequence, statement promptlang.ParsedStatement) instructionSequence {
	if statement.Value == nil {
		return sequence
	}
	for index, item := range sequence {
		switch item := item.(type) {
		case publishEventInstruction:
			item.event = eventWithSubmissionSource(item.event, statement)
			sequence[index] = item
		case executeCommandInstruction:
			item.outcome.statement = statement
			sequence[index] = item
		case startUserShellInstruction:
			item.statement = statement
			sequence[index] = item
		case readParticipantsInstruction:
			item.statement = statement
			sequence[index] = item
		}
	}
	return sequence
}

func eventWithSubmissionSource(event Event, statement promptlang.ParsedStatement) Event {
	switch event := event.(type) {
	case InputAccepted:
		event.Statement = statement
		return event
	case SubmissionSucceeded:
		event.Statement = statement
		return event
	case SubmissionFailed:
		event.Statement = statement
		event.Err = executionDiagnostic(event.Err, event.Code, executionErrorSpan(statement))
		return event
	case OperationFailed:
		event.Statement = statement
		event.Err = executionDiagnostic(event.Err, ErrorExecutionFailed, executionErrorSpan(statement))
		return event
	case UnknownCommand:
		event.Statement = statement
		if event.Err == nil {
			event.Err = executionDiagnostic(promptlang.UndefinedCommandError{Name: event.Name}, ErrorCode(promptlang.DiagnosticUndefinedCommand), executionErrorSpan(statement))
		}
		return event
	case ShellCompleted:
		event.Statement = statement
		event.Result.Err = executionDiagnostic(event.Result.Err, ErrorExecutionFailed, shellErrorSpan(statement))
		return event
	default:
		return event
	}
}

func executionDiagnostic(err error, code ErrorCode, span promptlang.Span) error {
	if err == nil {
		return nil
	}
	var diagnostic *promptlang.Diagnostic
	if errors.As(err, &diagnostic) {
		return err
	}
	return &promptlang.Diagnostic{
		Code: promptlang.DiagnosticCode(code), Span: span,
		Message: err.Error(), Cause: err,
	}
}

func executionErrorSpan(statement promptlang.ParsedStatement) promptlang.Span {
	if span, ok := commandReferenceErrorSpan(statement); ok {
		return span
	}
	switch value := statement.Value.(type) {
	case promptlang.Invite:
		return value.Alias.Span
	case promptlang.Remove:
		return value.Alias.Span
	case promptlang.Cancel:
		return value.Alias.Span
	case promptlang.Send:
		return value.Alias.Span
	case promptlang.PolicyEnable:
		return value.Name.Span
	case promptlang.Shell:
		return value.Program.Span
	default:
		return statement.Span
	}
}

func commandReferenceErrorSpan(statement promptlang.ParsedStatement) (promptlang.Span, bool) {
	switch value := statement.Value.(type) {
	case promptlang.UserDefinition:
		return value.Name.Span, true
	case promptlang.UserCommand:
		return value.Name.Span, true
	default:
		return promptlang.Span{}, false
	}
}

// Condition failures refer to the loop invocation, rather than its definition.
func shellErrorSpan(statement promptlang.ParsedStatement) promptlang.Span {
	if loop, ok := statement.Value.(promptlang.Loop); ok {
		return loop.Condition.Span
	}
	return executionErrorSpan(statement)
}

func (w *stageWorkflow) statement() promptlang.ParsedStatement {
	if w.active == nil {
		return promptlang.ParsedStatement{}
	}
	return w.active.source
}

func (w *loopWorkflow) statement() promptlang.ParsedStatement {
	if w.active == nil {
		return promptlang.ParsedStatement{}
	}
	return w.active.source
}
