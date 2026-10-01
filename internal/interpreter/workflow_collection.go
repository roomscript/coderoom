package interpreter

import (
	"github.com/trigosec/coderoom/internal/promptlang"
	"github.com/trigosec/coderoom/internal/session"
)

type workflowCollection struct {
	loop  loopWorkflow
	stage stageWorkflow
}

func (w *workflowCollection) submit(
	raw string,
	statement promptlang.Statement,
	commands *promptlang.Registry,
) (instructionSequence, bool) {
	switch statement := statement.(type) {
	case promptlang.Send, promptlang.Broadcast, promptlang.Handoff:
		return w.stage.start(raw, statement), true
	default:
	}
	loop, ok := statement.(promptlang.Loop)
	if !ok {
		return nil, false
	}
	return w.loop.start(raw, loop, commands), true
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
	case sharedSendPlanResult:
		return w.stage.handleCompletion(completion)
	case broadcastPlanResult:
		return w.stage.handleCompletion(completion)
	case participantStateResult:
		return w.stage.handleCompletion(completion)
	case handoffSourceResult:
		return w.stage.handleCompletion(completion)
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
		return w.stage.handleCompletion(completion)
	default:
		return nil
	}
}
