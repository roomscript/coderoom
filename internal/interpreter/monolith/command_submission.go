package monolith

import (
	"github.com/roomscript/coderoom/internal/interpreter/runtime"
	"github.com/roomscript/coderoom/internal/promptlang"
)

// SubmitCommand queues a command selected by the modular interpreter without
// consulting the legacy registry. The existing coordinator owns admission,
// preparation, launch, completion publication and shutdown. This bridge supplies
// participant reads, shell launch and native send admission; staging remains unwired.
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
	sequence := acceptedInputSequence(raw)
	// Addressed send owns its input record with actual routing at delivery.
	if _, send := op.statement.Value.(promptlang.Send); send {
		sequence = instructionSequence{publishEventInstruction{event: InputAccepted{Raw: raw}}}
	}
	sequence = append(sequence, goInvocationInstruction{
		command: op.command,
		outcome: submissionOutcome{raw: raw, operation: op.command.Name()},
	})
	e.runner.Run(withSubmissionSource(sequence, op.statement))
}
