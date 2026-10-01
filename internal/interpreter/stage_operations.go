package interpreter

type stageOperationResult struct {
	raw string
	ok  bool
}

type takeStageForEditOperation struct{ result chan stageOperationResult }
type discardStageOperation struct{ result chan stageOperationResult }
type interruptAndDispatchStageOperation struct{ result chan stageOperationResult }

// TakeStageForEdit atomically removes and returns the current staged input.
func (i *Interpreter) TakeStageForEdit() (string, bool) {
	result := make(chan stageOperationResult, 1)
	value := i.executor.runStageOperation(takeStageForEditOperation{result: result}, result)
	return value.raw, value.ok
}

// DiscardStage atomically abandons the current staged input.
func (i *Interpreter) DiscardStage() bool {
	result := make(chan stageOperationResult, 1)
	return i.executor.runStageOperation(discardStageOperation{result: result}, result).ok
}

// InterruptAndDispatchStage requests cancellation of the current stage's
// frozen blockers. Dispatch still waits for their causal lifecycle events.
func (i *Interpreter) InterruptAndDispatchStage() bool {
	result := make(chan stageOperationResult, 1)
	operation := interruptAndDispatchStageOperation{result: result}
	return i.executor.runStageOperation(operation, result).ok
}

func (e *interpreterExecutor) runStageOperation(
	operation operation,
	result <-chan stageOperationResult,
) stageOperationResult {
	if !e.enqueue(operation) {
		return stageOperationResult{}
	}
	select {
	case value := <-result:
		return value
	case <-e.done:
		select {
		case value := <-result:
			return value
		default:
			return stageOperationResult{}
		}
	}
}

func (op takeStageForEditOperation) apply(e *interpreterExecutor) {
	sequence, raw, ok := e.model.TakeStageForEdit()
	e.runner.Run(sequence)
	op.result <- stageOperationResult{raw: raw, ok: ok}
}

func (op discardStageOperation) apply(e *interpreterExecutor) {
	sequence, ok := e.model.DiscardStage()
	e.runner.Run(sequence)
	op.result <- stageOperationResult{ok: ok}
}

func (op interruptAndDispatchStageOperation) apply(e *interpreterExecutor) {
	sequence, ok := e.model.InterruptAndDispatchStage()
	e.runner.Run(sequence)
	op.result <- stageOperationResult{ok: ok}
}
