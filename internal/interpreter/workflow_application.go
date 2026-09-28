package interpreter

import (
	"github.com/trigosec/coderoom/internal/promptlang"
	"github.com/trigosec/coderoom/internal/session"
)

type workflowApplication struct{ loop loopWorkflow }

func (a *workflowApplication) handleSubmission(
	raw string,
	statement promptlang.Statement,
	commands *promptlang.Registry,
) (effectBatch, bool) {
	loop, ok := statement.(promptlang.Loop)
	if !ok {
		return effectBatch{}, false
	}
	return a.loop.start(raw, loop, commands), true
}

func (a *workflowApplication) handleSessionEvent(event session.Event) effectBatch {
	return a.loop.handleSessionEvent(event)
}

func (a *workflowApplication) handleCompletion(completion workflowCompletion) effectBatch {
	switch completion := completion.(type) {
	case sessionCompletion:
		if completion.target.kind == workflowLoop {
			return a.loop.handleSessionCompletion(completion)
		}
	case shellCompletion:
		if completion.target.kind == workflowLoop {
			return a.loop.handleShellCompletion(completion)
		}
	}
	return effectBatch{}
}
