package interpreter

import (
	"github.com/roomscript/coderoom/internal/interpreter/runtime"
	"github.com/roomscript/coderoom/internal/promptlang"
)

// Submit queues prompt-language input, or returns ErrClosed after shutdown.
func (i *Interpreter) Submit(raw string) error {
	statement, parseErr := promptlang.Parse(raw)
	//nolint:wrapcheck // Preserve queue admission error identity.
	return i.fallback.Schedule(func() {
		var command runtime.Command
		if parseErr == nil {
			command = i.selectCommand(statement)
		}
		if i.rejectStagedInput(raw, command) {
			return
		}
		if parseErr != nil {
			i.fallback.Publish(InputRejected{Raw: raw, Code: ErrorInvalidInput, Err: parseErr})
			return
		}
		if command == nil {
			i.fallback.Publish(InputRejected{Raw: raw, Code: ErrorInvalidInput, Err: &promptlang.Diagnostic{
				Code: promptlang.DiagnosticCode(ErrorInvalidInput), Span: statement.Span,
				Message: "command is not registered",
			}})
			return
		}
		i.executeCommand(command, statement)
	})
}

// TakeStageForEdit atomically removes and returns a still-waiting draft.
func (i *Interpreter) TakeStageForEdit() (string, bool) {
	result := i.runStageOperation(i.takeStageForEdit)
	return result.raw, result.ok
}

// DiscardStage atomically abandons still-waiting work.
func (i *Interpreter) DiscardStage() bool {
	return i.runStageOperation(i.discardStage).ok
}

// InterruptAndDispatchStage cancels frozen active blockers, then keeps waiting
// for actual readiness. Successful cancellations are not requested again.
func (i *Interpreter) InterruptAndDispatchStage() bool {
	return i.runStageOperation(i.interruptStage).ok
}

// ResolveApproval queues a structured response to the active approval.
func (i *Interpreter) ResolveApproval(id int64, choice ApprovalChoice) error {
	return i.fallback.ResolveApproval(id, choice) //nolint:wrapcheck // Preserve existing facade error identity.
}
