package interpreter

// User requests enter the serialized interpreter path through these methods.

// Submit queues prompt-language input. It returns ErrClosed if ownership cannot
// be accepted because shutdown has begun.
func (i *Interpreter) Submit(raw string) error {
	return i.executor.submit(raw)
}

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

// ResolveApproval queues a structured response to the active approval.
func (i *Interpreter) ResolveApproval(id int64, choice ApprovalChoice) error {
	return i.executor.resolveApproval(id, choice)
}
