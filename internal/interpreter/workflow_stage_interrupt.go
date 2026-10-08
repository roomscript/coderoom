package interpreter

import (
	"fmt"
	"slices"

	"github.com/roomscript/coderoom/internal/room"
)

// stageInterruption owns cancellation correlation and successful acknowledgements.
// Readiness remains owned by the stage; cancellation success does not imply idle.
type stageInterruption struct {
	requested bool
	pending   map[workflowRef]string
	cancelled []string
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
	aliases := interruptibleStageAliases(state)
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
	completion sessionCompletion,
) (instructionSequence, bool) {
	if w.active == nil {
		return nil, false
	}
	handled := w.active.interruption.completeCancellation(completion)
	if !handled {
		return nil, false
	}
	sequence := instructionSequence{requestSnapshotInstruction{}}
	if completion.err == nil {
		return sequence, true
	}
	return append(sequence, publishEventInstruction{event: OperationFailed{
		Operation: "interrupt staged submission", Err: completion.err,
	}}), true
}

// completeCancellation consumes a reply and remembers successful cancellations.
// Failed aliases remain eligible for retry; duplicates and stale replies do nothing.
func (s *stageInterruption) completeCancellation(completion sessionCompletion) bool {
	alias, ok := s.pending[completion.target]
	if !ok {
		return false
	}
	delete(s.pending, completion.target)
	if completion.err == nil {
		s.cancelled = append(s.cancelled, alias)
		slices.Sort(s.cancelled)
	}
	return true
}

func interruptibleStageAliases(state *stageState) []string {
	waiting := state.requirements.waitingAliases()
	var aliases []string
	for _, value := range state.requirements.participants {
		if value.view().HasActiveTurn() && value.view().IsCancellable() &&
			slices.Contains(waiting, value.alias) && !slices.Contains(state.interruption.cancelled, value.alias) {
			aliases = append(aliases, value.alias)
		}
	}
	slices.Sort(aliases)
	return aliases
}
