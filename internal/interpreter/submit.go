package interpreter

import (
	"errors"

	"github.com/roomscript/coderoom/internal/promptlang"
)

type submitOperation struct {
	raw string
}

func (e *interpreterExecutor) submit(raw string) error {
	if !e.enqueue(submitOperation{raw: raw}) {
		return ErrClosed
	}
	return nil
}

func (op submitOperation) apply(e *interpreterExecutor) {
	e.handleInput(op.raw)
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

func commandName(statement promptlang.Statement) string {
	invocation, ok := statement.(promptlang.CommandInvocation)
	if !ok {
		return ""
	}
	return invocation.Name.Value
}
