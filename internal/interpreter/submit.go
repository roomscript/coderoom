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
	return i.executor.submit(raw, nil)
}

// SubmitWithFallback queues input with a temporary legacy session command and
// returns ErrClosed if ownership cannot be accepted. Native handlers take
// precedence once they are introduced.
func (i *Interpreter) SubmitWithFallback(raw string, fallback session.Command) error {
	return i.executor.submit(raw, fallback)
}

func (e *interpreterExecutor) submit(raw string, fallback session.Command) error {
	if !e.enqueue(submitOperation{raw: raw, fallback: fallback}) {
		return ErrClosed
	}
	return nil
}

func (op submitOperation) apply(e *interpreterExecutor) {
	if preflight := e.model.PreflightSubmission(op.raw); len(preflight) != 0 {
		e.runner.Run(preflight)
		return
	}
	statement, err := promptlang.Parse(op.raw)
	if err != nil {
		e.publish(InputRejected{Raw: op.raw, Code: ErrorInvalidInput, Err: err})
		return
	}
	e.runner.Run(e.model.Submit(op.raw, statement, op.fallback))
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
