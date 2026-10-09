package interpreter

import (
	"github.com/roomscript/coderoom/internal/promptlang"
	"github.com/roomscript/coderoom/internal/shell"
)

// Executor mechanics run shell work away from the serialized interpreter and
// enqueue results back onto it. Shutdown cancels and joins these goroutines.
type shellCompletedOperation struct {
	raw       string
	statement promptlang.ParsedStatement
	command   string
	result    shell.Result
}

func (e *interpreterExecutor) setShellRunner(runner ShellRunner) {
	e.runShell = runner
}

func (e *interpreterExecutor) launchUserShell(raw, command, program string, statement promptlang.ParsedStatement) {
	e.shellWG.Add(1)
	go func() {
		defer e.shellWG.Done()
		result := e.runShell.Run(e.lifetime, e.cwd, program)
		e.enqueue(shellCompletedOperation{raw: raw, statement: statement, command: command, result: result})
	}()
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
