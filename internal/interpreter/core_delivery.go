package interpreter

// applySessionOutcome separates cancellation acknowledgements from delivery.
// Cancellation changes interruption bookkeeping; only a matching delivery result
// finishes the retained plan. Neither result means participant work is complete.
func (w *stageWorkflow) applySessionOutcome(result sessionOutcome) instructionSequence {
	if actions, handled := w.handlePendingInterruptCompletion(result); handled {
		return actions
	}
	return w.finishDelivery(result)
}

// finishDelivery runs after the runner records delivered input and applies the
// command's causal session events. Report actual recipients (including partial
// delivery) and submission success or failure after releasing pending work.
func (w *stageWorkflow) finishDelivery(result sessionOutcome) instructionSequence {
	if !w.matches(result.target) || w.active.phase != stageDispatching {
		return nil
	}
	state := w.active
	w.active = nil
	actions := state.deliveryEvents(result)
	return append(actions, state.deliveryOutcome(result)...)
}
