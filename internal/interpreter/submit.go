package interpreter

import (
	"errors"

	"github.com/trigosec/coderoom/internal/promptlang"
	"github.com/trigosec/coderoom/internal/session"
)

type submitOperation struct {
	raw      string
	fallback session.Command
}

// Submit queues prompt-language input. It returns ErrClosed if ownership cannot
// be accepted because shutdown has begun.
func (i *Interpreter) Submit(raw string) error {
	if !i.enqueue(submitOperation{raw: raw}) {
		return ErrClosed
	}
	return nil
}

// SubmitWithFallback queues input with a temporary legacy session command and
// returns ErrClosed if ownership cannot be accepted. Native handlers take
// precedence once they are introduced.
func (i *Interpreter) SubmitWithFallback(raw string, fallback session.Command) error {
	if !i.enqueue(submitOperation{raw: raw, fallback: fallback}) {
		return ErrClosed
	}
	return nil
}

func (op submitOperation) apply(i *Interpreter) {
	if i.stagePending {
		i.publish(InputRejected{Raw: op.raw, Code: ErrorStagePending, Err: ErrStagePending})
		return
	}
	statement, err := promptlang.Parse(op.raw)
	if err != nil {
		i.publish(InputRejected{Raw: op.raw, Code: ErrorInvalidInput, Err: err})
		return
	}
	if i.executeNative(op.raw, statement) {
		return
	}
	if op.fallback == nil {
		i.publish(UnknownCommand{Raw: op.raw, Name: commandName(statement)})
		return
	}
	i.executeFallback(op.raw, statement, op.fallback)
}

func (i *Interpreter) executeNative(raw string, statement promptlang.Statement) bool {
	if batch, handled := i.workflows.handleSubmission(raw, statement, i.commands); handled {
		i.applyEffectBatch(batch)
		return true
	}
	return i.executeNativeSession(raw, statement) ||
		i.executeNativeShell(raw, statement) ||
		i.executeNativeControl(raw, statement)
}

func (i *Interpreter) executeNativeSession(raw string, statement promptlang.Statement) bool {
	switch statement := statement.(type) {
	case promptlang.Invite:
		i.executeInvite(raw, statement)
		return true
	case promptlang.Remove:
		i.executeRemove(raw, statement)
		return true
	case promptlang.Cancel:
		i.executeCancel(raw, statement)
		return true
	case promptlang.PolicyEnable:
		i.executePolicyEnable(raw, statement)
		return true
	default:
		return false
	}
}

func (i *Interpreter) executeNativeShell(raw string, statement promptlang.Statement) bool {
	switch statement := statement.(type) {
	case promptlang.Shell:
		i.executeShell(raw, statement.Program, statement.Program)
		return true
	case promptlang.CommandDefinition:
		i.executeCommandDefinition(raw, statement)
		return true
	case promptlang.CommandInvocation:
		i.executeCommandInvocation(raw, statement)
		return true
	default:
		return false
	}
}

func (i *Interpreter) executeNativeControl(raw string, statement promptlang.Statement) bool {
	switch statement.(type) {
	case promptlang.Who:
		i.executeWho(raw)
		return true
	case promptlang.Help:
		i.executeHelp(raw)
		return true
	case promptlang.Quit:
		i.executeQuit(raw)
		return true
	default:
		return false
	}
}

func (i *Interpreter) executeFallback(raw string, statement promptlang.Statement, fallback session.Command) {
	i.room.AppendUserInputRecord(raw, nil)
	i.publish(InputAccepted{Raw: raw})
	err := i.session.Execute(fallback)
	i.drainSessionEvents(false)
	i.publish(StateChanged{Snapshot: i.captureSnapshot()})
	if err != nil {
		i.publish(SubmissionFailed{
			Raw:       raw,
			Operation: submissionOperation(statement),
			Code:      ErrorExecutionFailed,
			Err:       err,
		})
		return
	}
	i.publish(SubmissionSucceeded{Raw: raw})
}

func submissionErrorCode(err error) ErrorCode {
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

func submissionOperation(statement promptlang.Statement) string {
	return commandName(statement)
}

func commandName(statement promptlang.Statement) string {
	invocation, ok := statement.(promptlang.CommandInvocation)
	if !ok {
		return ""
	}
	return invocation.Name
}
