package interpreter

import (
	"github.com/roomscript/coderoom/internal/promptlang"
	"github.com/roomscript/coderoom/internal/session"
)

// Stage orchestration: begin synchronous preparation, suspend a retained plan,
// and resume it on session facts. Send decisions live in core_send.go; delivery
// results finish through core_delivery.go.
func (w *stageWorkflow) start(raw string, statement promptlang.Statement) instructionSequence {
	w.nextGeneration++
	w.active = &stageState{
		generation:        w.nextGeneration,
		raw:               raw,
		statement:         statement,
		phase:             stagePlanning,
		submissionPending: true,
	}
	if handoff, ok := statement.(promptlang.Handoff); ok {
		w.active.handoff = &handoffStage{action: handoff}
	}
	ref := w.nextRef()
	w.active.pending = ref
	if send, ok := statement.(promptlang.Send); ok {
		w.active.send = &sendPlan{action: send}
		return instructionSequence{prepareSendInstruction{target: ref, alias: send.Alias}}
	}
	if broadcast, ok := statement.(promptlang.Broadcast); ok {
		w.active.broadcast = &broadcast
		return instructionSequence{planBroadcastInstruction{target: ref}}
	}
	return instructionSequence{readParticipantStateInstruction{target: ref}}
}

// retainPlanUntilReady is the suspension point. After publishing queued success,
// the stage waits for later lifecycle/output events to resume work.
func (w *stageWorkflow) retainPlanUntilReady() instructionSequence {
	w.active.phase = stageWaiting
	w.active.submissionPending = false
	return instructionSequence{
		requestSnapshotInstruction{},
		publishEventInstruction{event: SubmissionSucceeded{Raw: w.active.raw}},
	}
}

func (w *stageWorkflow) handleSessionEvent(event session.Event) instructionSequence {
	if w.active == nil {
		return nil
	}
	if w.active.phase == stageDispatching {
		if w.active.handoff != nil {
			w.active.handoff.captureCompletion(event)
		}
		return nil
	}
	if w.active.phase != stageWaiting {
		return nil
	}
	if w.active.handoff != nil {
		return w.handleHandoffSessionEvent(event)
	}
	if !w.active.requirements.applySessionEvent(event) {
		return nil
	}
	return w.advanceWaitingStage()
}

func (w *stageWorkflow) advanceWaitingStage() instructionSequence {
	if w.active.send != nil {
		return w.resumeSendOnReadiness()
	}
	return w.resumeBroadcastOnReadiness()
}

// Preparation results advance only the action that requested the facts.
func (w *stageWorkflow) handleParticipantState(result participantStateResult) instructionSequence {
	if !w.matches(result.target) {
		return nil
	}
	if w.active.broadcast != nil {
		return w.prepareBroadcast(result)
	}
	if w.active.handoff != nil {
		return w.prepareHandoff(result)
	}
	return nil // Sends obtain readiness in their synchronous preparation result.
}
