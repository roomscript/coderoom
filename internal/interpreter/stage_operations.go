package interpreter

type stageOperationResult struct {
	raw string
	ok  bool
}

type takeStageForEditOperation struct{ result chan stageOperationResult }
type discardStageOperation struct{ result chan stageOperationResult }
type interruptAndDispatchStageOperation struct{ result chan stageOperationResult }

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
