package interpreter

import (
	"github.com/trigosec/coderoom/internal/promptlang"
	"github.com/trigosec/coderoom/internal/session"
)

type workflowCollection struct{ loop loopWorkflow }

func (w *workflowCollection) submit(
	raw string,
	statement promptlang.Statement,
	commands *promptlang.Registry,
) (instructionSequence, bool) {
	loop, ok := statement.(promptlang.Loop)
	if !ok {
		return nil, false
	}
	return w.loop.start(raw, loop, commands), true
}

func (w *workflowCollection) applySessionEvent(event session.Event) instructionSequence {
	return w.loop.handleSessionEvent(event)
}

func (w *workflowCollection) applyCompletion(completion workflowCompletion) instructionSequence {
	switch completion := completion.(type) {
	case sessionCompletion:
		if completion.target.kind == workflowLoop {
			return w.loop.handleSessionCompletion(completion)
		}
	case shellCompletion:
		if completion.target.kind == workflowLoop {
			return w.loop.handleShellCompletion(completion)
		}
	}
	return nil
}
