package interpreter

import (
	"fmt"
	"slices"

	"github.com/roomscript/coderoom/internal/promptlang"
	"github.com/roomscript/coderoom/internal/room"
)

// prepareSend is synchronous: retain the frozen plan, validate it, accept input,
// then dispatch now or suspend until a participant readiness event arrives.
func (w *stageWorkflow) prepareSend(result sendPlanResult) instructionSequence {
	if !w.matches(result.target) || w.active.phase != stagePlanning {
		return nil
	}
	send, ok := w.active.statement.(promptlang.Send)
	if !ok {
		return nil
	}
	w.freezeSendPlan(result)
	if len(w.active.routing) == 0 {
		return w.rejectSendPlanning(SubmissionFailed{
			Raw: w.active.raw, Operation: "staged dispatch", Code: ErrorExecutionFailed,
			Err: errNoStageTargets,
		})
	}
	if slices.Contains(w.active.unavailable, send.Alias) {
		return w.rejectSendPlanning(unavailableStageFailure(w.active))
	}

	sequence := acceptedStageInputSequence(w.active.raw, w.active.routing)
	if len(w.active.notReadyAliases) != 0 {
		return append(sequence, w.retainPlanUntilReady()...)
	}
	return append(sequence, w.startSendDispatch(send))
}

func (w *stageWorkflow) freezeSendPlan(result sendPlanResult) {
	w.active.plan = result.plan
	w.active.routing = slices.Clone(result.targets)
	w.active.readinessRequirements = freezeReadinessRequirements(w.active, result.participants)
	w.active.notReadyAliases, w.active.unavailable = stageReadiness(w.active.readinessRequirements, w.active.routing)
}

func (w *stageWorkflow) rejectSendPlanning(failure SubmissionFailed) instructionSequence {
	w.active = nil
	return instructionSequence{publishEventInstruction{event: failure}}
}

// resumeSendOnReadiness is the entry point after a retained send returns control.
func (w *stageWorkflow) resumeSendOnReadiness(send promptlang.Send) instructionSequence {
	state := w.active
	state.notReadyAliases = notReadyAliases(state.readinessRequirements, state.unavailable)
	if slices.Contains(state.unavailable, send.Alias) {
		return w.discardUnavailableStage()
	}
	if len(state.notReadyAliases) != 0 {
		return instructionSequence{requestSnapshotInstruction{}}
	}
	return instructionSequence{w.startSendDispatch(send), requestSnapshotInstruction{}}
}

func (w *stageWorkflow) startSendDispatch(send promptlang.Send) executeSessionInstruction {
	state := w.active
	state.phase = stageDispatching
	state.dispatchRouting = activeAliases(state.routing, state.unavailable)
	state.pending = w.nextRef()
	return executeSessionInstruction{
		target: state.pending,
		request: executePlannedParticipantSendRequest{
			plan:    state.plan.DiscardUnavailableNoticeRecipients(state.unavailable),
			message: send.Text,
			notice:  fmt.Sprintf("@%s: %s", send.Alias, send.Text),
		},
		recordsOnSuccess: []room.Record{{Kind: room.KindUserInput, Text: state.raw}},
	}
}
