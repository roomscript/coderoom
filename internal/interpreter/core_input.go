package interpreter

import "github.com/roomscript/coderoom/internal/promptlang"

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
	e.runner.Run(e.model.Submit(raw, statement))
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

// Submit selects the request workflow. It prepares its actions and requirements;
// returned instructions execute ready work or publish the retained-stage outcome.
func (m *interpreterModel) Submit(
	raw string,
	statement promptlang.Statement,
) instructionSequence {
	if sequence, handled := m.submitCommand(raw, statement); handled {
		return sequence
	}
	return instructionSequence{publishEventInstruction{event: UnknownCommand{
		Raw: raw, Name: commandName(statement),
	}}}
}
