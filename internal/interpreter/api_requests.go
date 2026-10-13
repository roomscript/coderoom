package interpreter

// Submit queues prompt-language input, or returns ErrClosed after shutdown.
func (i *Interpreter) Submit(raw string) error {
	return i.fallback.Submit(raw) //nolint:wrapcheck // Preserve existing facade error identity.
}

// TakeStageForEdit atomically removes and returns the current staged input.
func (i *Interpreter) TakeStageForEdit() (string, bool) { return i.fallback.TakeStageForEdit() }

// DiscardStage atomically abandons the current staged input.
func (i *Interpreter) DiscardStage() bool { return i.fallback.DiscardStage() }

// InterruptAndDispatchStage requests cancellation of the stage's frozen blockers.
// Dispatch still waits for their causal lifecycle events.
func (i *Interpreter) InterruptAndDispatchStage() bool {
	return i.fallback.InterruptAndDispatchStage()
}

// ResolveApproval queues a structured response to the active approval.
func (i *Interpreter) ResolveApproval(id int64, choice ApprovalChoice) error {
	return i.fallback.ResolveApproval(id, choice) //nolint:wrapcheck // Preserve existing facade error identity.
}
