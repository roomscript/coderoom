// Package interpreter implements coderoom's UI-independent application layer.
package interpreter

import "github.com/roomscript/coderoom/internal/promptlang"

// Option configures an Interpreter.
type Option func(*Interpreter)

// Interpreter is the public facade and composition root for the interpreter.
type Interpreter struct {
	model    *interpreterModel
	executor *interpreterExecutor
}

// ResolveCommand returns a room-scoped command body. It returns ErrClosed if
// shutdown prevents acceptance.
func (i *Interpreter) ResolveCommand(invocation promptlang.UserCommand) (promptlang.Shell, error) {
	return i.executor.resolveCommand(invocation)
}

// Snapshot returns a detached view of current application state.
func (i *Interpreter) Snapshot() Snapshot {
	return i.executor.snapshot()
}

// Close stops the interpreter and its owned background work.
func (i *Interpreter) Close() {
	i.executor.close()
}
