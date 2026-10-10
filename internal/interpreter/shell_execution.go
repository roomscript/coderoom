package interpreter

import (
	"github.com/roomscript/coderoom/internal/interpreter/runtime"
	"github.com/roomscript/coderoom/internal/promptlang"
	"github.com/roomscript/coderoom/internal/shell"
)

// Executor mechanics run shell work away from the serialized interpreter and
// enqueue results back onto it. Shutdown cancels and joins these goroutines.
type shellCompletedOperation struct {
	raw        string
	statement  promptlang.ParsedStatement
	command    string
	result     shell.Result
	completion runtime.Completion
}

func (e *interpreterExecutor) setShellRunner(runner ShellRunner) {
	e.runShell = runner
}

// The adapter retains the legacy event's result before the command completes.
// Both writes and callback reads happen on the same owned worker.
type userShellLauncher struct {
	executor *interpreterExecutor
	result   shell.Result
}

func (l *userShellLauncher) Cwd() string { return l.executor.cwd }

func (l *userShellLauncher) Go(program string, complete func(shell.Result)) error {
	e := l.executor
	e.shellWG.Add(1)
	go func() {
		defer e.shellWG.Done()
		l.result = e.runShell.Run(e.lifetime, e.cwd, program)
		complete(l.result)
	}()
	return nil
}

func (e *interpreterExecutor) startWorkflowShell(value startShellInstruction) {
	e.shellWG.Add(1)
	go func() {
		defer e.shellWG.Done()
		result := e.runShell.Run(e.lifetime, e.cwd, value.request.program)
		e.enqueue(workflowShellCompletedOperation{
			target: value.target, request: value.request, result: result,
		})
	}()
}

type shellRequest struct {
	raw       string
	statement promptlang.ParsedStatement
	command   string
	program   string
}

type workflowShellCompletedOperation struct {
	target  workflowRef
	request shellRequest
	result  shell.Result
}
