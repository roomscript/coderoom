package interpreter

// loopWorkflow owns the active loop and correlates results with its current step.
// The domain algorithm is in core_loop.go; retained action details are in loop_state.go.
type loopWorkflow struct {
	active         *loopState
	nextGeneration uint64
	nextRequestID  uint64
}

func (w *loopWorkflow) nextRef(generation uint64) workflowRef {
	w.nextRequestID++
	return workflowRef{kind: workflowLoop, generation: generation, requestID: w.nextRequestID}
}

func (w *loopWorkflow) matches(target workflowRef, phase loopPhase) bool {
	return w.active != nil && w.active.generation == target.generation &&
		w.active.pending == target && w.active.phase == phase
}
