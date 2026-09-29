// Package interpreter implements coderoom's UI-independent application layer.
package interpreter

import (
	"context"

	"github.com/trigosec/coderoom/internal/promptlang"
	"github.com/trigosec/coderoom/internal/session"
)

// Option configures an Interpreter.
type Option func(*Interpreter)

// Interpreter is the public facade and composition root for the interpreter.
type Interpreter struct {
	model    *interpreterModel
	executor *interpreterExecutor
	approval *approvalSnapshotState
}

// New starts an Interpreter backed by sess.
func New(ctx context.Context, sess SessionController, cwd string, opts ...Option) *Interpreter {
	if ctx == nil {
		ctx = context.Background()
	}
	model := newInterpreterModel()
	approval := &approvalSnapshotState{}
	i := &Interpreter{
		model:    model,
		approval: approval,
	}
	i.executor = newInterpreterExecutor(ctx, sess, cwd, model, approval)
	for _, opt := range opts {
		opt(i)
	}
	i.executor.Start()
	return i
}

// ResolveCommand returns a room-scoped command body. It returns ErrClosed if
// shutdown prevents acceptance.
func (i *Interpreter) ResolveCommand(invocation promptlang.CommandInvocation) (promptlang.Shell, error) {
	return i.executor.resolveCommand(invocation)
}

// ExecuteLegacy synchronously executes a transitional session command on the
// interpreter loop. It returns ErrClosed if shutdown prevents acceptance.
func (i *Interpreter) ExecuteLegacy(command session.Command) error {
	return i.executor.executeLegacy(command)
}

// Snapshot returns a detached view of current application state.
func (i *Interpreter) Snapshot() Snapshot {
	return i.executor.snapshot()
}

// AddObserver registers an application event observer.
func (i *Interpreter) AddObserver(observer Observer) {
	i.executor.addObserver(observer)
}

// Close stops the interpreter and its owned background work.
func (i *Interpreter) Close() {
	i.executor.close()
}
