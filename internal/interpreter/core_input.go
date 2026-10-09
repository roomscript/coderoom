package interpreter

import (
	"github.com/roomscript/coderoom/internal/promptlang"
	"github.com/roomscript/coderoom/internal/room"
)

// handleInput parses once, checks admission, then executes model decisions.
// A retained stage returns control; later session events resume it.
func (e *interpreterExecutor) handleInput(raw string) {
	statement, err := promptlang.Parse(raw)
	if !isPresentationStatement(statement.Value) {
		if rejection := e.model.CheckInputAllowed(raw); len(rejection) != 0 {
			e.runner.Run(rejection)
			return
		}
	}
	if err != nil {
		e.publish(InputRejected{Raw: raw, Code: ErrorInvalidInput, Err: err})
		return
	}
	actions := e.model.PrepareRequest(raw, statement)
	e.runner.Run(actions)
}

// Presentation commands remain available while delivery is staged.
// Admission still takes precedence over syntax errors for other submissions.
func isPresentationStatement(statement promptlang.Statement) bool {
	switch statement.(type) {
	case promptlang.DebugView, promptlang.DebugRows:
		return true
	default:
		return false
	}
}

// CheckInputAllowed checks admission before validation and execution.
func (m *interpreterModel) CheckInputAllowed(raw string) instructionSequence {
	if !m.workflows.stage.pending() {
		return nil
	}
	return instructionSequence{publishEventInstruction{event: InputRejected{
		Raw: raw, Code: ErrorStagePending, Err: &promptlang.Diagnostic{
			Code: promptlang.DiagnosticCode(ErrorStagePending), Span: promptlang.Span{End: len(raw)},
			Message: ErrStagePending.Error(), Cause: ErrStagePending,
		},
	}}}
}

// PrepareRequest selects the workflow and returns its next actions. Preparation
// reads run synchronously; unmet requirements retain work for a later event.
func (m *interpreterModel) PrepareRequest(
	raw string,
	statement promptlang.ParsedStatement,
) instructionSequence {
	previousStage, previousLoop := m.workflows.stage.active, m.workflows.loop.active
	if sequence, handled := m.prepareCommand(raw, statement.Value); handled {
		if active := m.workflows.stage.active; active != nil && active != previousStage {
			active.source = statement
		}
		if active := m.workflows.loop.active; active != nil && active != previousLoop {
			active.source = statement
		}
		return withSubmissionSource(sequence, statement)
	}
	return withSubmissionSource(instructionSequence{publishEventInstruction{event: UnknownCommand{
		Raw: raw, Name: commandName(statement.Value),
	}}}, statement)
}

func acceptedInputSequence(raw string) instructionSequence {
	return instructionSequence{
		appendRecordInstruction{record: room.Record{Kind: room.KindUserInput, Text: raw}},
		publishEventInstruction{event: InputAccepted{Raw: raw}},
	}
}
