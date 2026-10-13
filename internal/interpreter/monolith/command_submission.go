package monolith

import (
	"github.com/roomscript/coderoom/internal/interpreter/runtime"
	"github.com/roomscript/coderoom/internal/promptlang"
)

// SubmitCommand queues a command selected by the modular interpreter without
// consulting the legacy registry. The existing coordinator owns admission,
// preparation, launch, completion publication and shutdown. This bridge supplies
// participant reads and shell launch; Session delivery and staging remain unwired.
func (i *Interpreter) SubmitCommand(command runtime.Command, statement promptlang.ParsedStatement) error {
	if !i.executor.enqueue(commandSubmissionOperation{command: command, statement: statement}) {
		return ErrClosed
	}
	return nil
}

type commandSubmissionOperation struct {
	command   runtime.Command
	statement promptlang.ParsedStatement
}

func (op commandSubmissionOperation) apply(e *interpreterExecutor) {
	raw := op.statement.Raw
	if rejection := e.model.CheckInputAllowed(raw); len(rejection) != 0 {
		e.runner.Run(rejection)
		return
	}
	sequence := append(acceptedInputSequence(raw), goInvocationInstruction{
		command: op.command,
		outcome: submissionOutcome{raw: raw, operation: op.command.Name()},
	})
	e.runner.Run(withSubmissionSource(sequence, op.statement))
}
