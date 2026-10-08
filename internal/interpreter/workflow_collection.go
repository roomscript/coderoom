package interpreter

import (
	"github.com/roomscript/coderoom/internal/session"
)

type workflowCollection struct {
	loop  loopWorkflow
	stage stageWorkflow
}

func (w *workflowCollection) applySessionEvent(event session.Event) instructionSequence {
	sequence := w.loop.handleSessionEvent(event)
	return append(sequence, w.stage.handleSessionEvent(event)...)
}

func (w *workflowCollection) applyCompletion(completion workflowCompletion) instructionSequence {
	switch completion := completion.(type) {
	case sessionCompletion:
		return w.applySessionCompletion(completion)
	case shellCompletion:
		if completion.target.kind == workflowLoop {
			return w.loop.handleShellCompletion(completion)
		}
	case sendPlanResult:
		return w.stage.prepareSend(completion)
	case broadcastPlanResult:
		return w.stage.handleBroadcastPlan(completion)
	case participantStateResult:
		return w.stage.handleParticipantState(completion)
	case handoffSourceResult:
		return w.stage.handleHandoffSource(completion)
	}
	return nil
}

func (w *workflowCollection) applySessionCompletion(
	completion sessionCompletion,
) instructionSequence {
	switch completion.target.kind {
	case workflowLoop:
		return w.loop.handleSessionCompletion(completion)
	case workflowStage:
		return w.stage.handleSessionCompletion(completion)
	default:
		return nil
	}
}
