package interpreter

import (
	"github.com/roomscript/coderoom/internal/promptlang"
	"github.com/roomscript/coderoom/internal/room"
)

// handleInput checks the request, interprets it, then executes model decisions.
// A retained stage returns control; later session events resume it.
func (e *interpreterExecutor) handleInput(raw string) {
	if rejection := e.model.CheckInputAllowed(raw); len(rejection) != 0 {
		e.runner.Run(rejection)
		return
	}
	statement, err := promptlang.Parse(raw)
	if err != nil {
		e.publish(InputRejected{Raw: raw, Code: ErrorInvalidInput, Err: err})
		return
	}
	actions := e.model.PrepareRequest(raw, statement)
	e.runner.Run(actions)
}

// CheckInputAllowed checks whether new input is allowed before parsing.
func (m *interpreterModel) CheckInputAllowed(raw string) instructionSequence {
	if !m.workflows.stage.pending() {
		return nil
	}
	return instructionSequence{publishEventInstruction{event: InputRejected{
		Raw: raw, Code: ErrorStagePending, Err: ErrStagePending,
	}}}
}

// PrepareRequest selects the workflow and returns its next actions. Preparation
// reads run synchronously; unmet requirements retain work for a later event.
func (m *interpreterModel) PrepareRequest(
	raw string,
	statement promptlang.Statement,
) instructionSequence {
	if sequence, handled := m.prepareCommand(raw, statement); handled {
		return sequence
	}
	return instructionSequence{publishEventInstruction{event: UnknownCommand{
		Raw: raw, Name: commandName(statement),
	}}}
}

func acceptedInputSequence(raw string) instructionSequence {
	return instructionSequence{
		appendRecordInstruction{record: room.Record{Kind: room.KindUserInput, Text: raw}},
		publishEventInstruction{event: InputAccepted{Raw: raw}},
	}
}
