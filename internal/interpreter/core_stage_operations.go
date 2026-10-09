package interpreter

import (
	"fmt"

	"github.com/roomscript/coderoom/internal/room"
)

// Stage requests atomically edit/discard retained work or request interruption.
// Cancellation results update acknowledgements; readiness events resume delivery.
func (w *stageWorkflow) takeForEdit() (instructionSequence, string, bool) {
	if w.active == nil {
		return nil, "", false
	}
	raw := w.active.raw
	w.active = nil
	return instructionSequence{requestSnapshotInstruction{}}, raw, true
}

func (w *stageWorkflow) discard() (instructionSequence, bool) {
	if w.active == nil {
		return nil, false
	}
	w.active = nil
	return instructionSequence{requestSnapshotInstruction{}}, true
}

func (w *stageWorkflow) interruptAndDispatch() (instructionSequence, bool) {
	if w.active == nil || len(w.active.interruption.pending) != 0 {
		return nil, false
	}
	state := w.active
	if state.requirements.isReady() {
		if state.interruption.requested {
			return nil, false
		}
		state.interruption.requested = true
		return w.advanceInterruptedStage(), true
	}
	aliases := state.requirements.interruptibleAliases(state.interruption.cancelled)
	if len(aliases) == 0 {
		return nil, false
	}
	state.interruption.requested = true
	state.interruption.pending = make(map[workflowRef]string, len(aliases))
	sequence := make(instructionSequence, 0, len(aliases)+1)
	for _, alias := range aliases {
		ref := w.nextRef()
		state.interruption.pending[ref] = alias
		sequence = append(sequence, executeSessionInstruction{
			target: ref, request: cancelRequest{alias: alias},
			recordsOnSuccess: []room.Record{{
				Kind: room.KindSystem, Text: fmt.Sprintf("[→ %s] interrupt requested", alias),
			}},
		})
	}
	return append(sequence, requestSnapshotInstruction{}), true
}

func (w *stageWorkflow) advanceInterruptedStage() instructionSequence {
	if w.active.handoff != nil {
		if w.active.handoff.sourceNeedsCompletion {
			return instructionSequence{requestSnapshotInstruction{}}
		}
		return instructionSequence{w.readHandoffSourceInstruction(), requestSnapshotInstruction{}}
	}
	if w.active.send != nil {
		return instructionSequence{w.startSendDispatch(), requestSnapshotInstruction{}}
	}
	return instructionSequence{w.startBroadcastDispatch(), requestSnapshotInstruction{}}
}

func (w *stageWorkflow) handlePendingInterruptCompletion(
	outcome sessionOutcome,
) (instructionSequence, bool) {
	if w.active == nil {
		return nil, false
	}
	handled := w.active.interruption.completeCancellation(outcome)
	if !handled {
		return nil, false
	}
	sequence := instructionSequence{requestSnapshotInstruction{}}
	if outcome.err == nil {
		return sequence, true
	}
	return append(sequence, publishEventInstruction{event: OperationFailed{
		Operation: "interrupt staged submission", Err: outcome.err,
	}}), true
}
