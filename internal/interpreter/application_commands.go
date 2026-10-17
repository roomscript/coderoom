package interpreter

import (
	"context"
	"fmt"

	"github.com/roomscript/coderoom/internal/interpreter/runtime"
	"github.com/roomscript/coderoom/internal/promptlang"
)

// ApplicationCommand is a named, argument-free command implemented by the caller.
// Run executes in the ordered publication dispatcher, after earlier interpreter
// events reach observers. It must return promptly and must not wait for interpreter
// processing or call blocking APIs such as Snapshot, ResolveCommand or Close. Returning
// nil acknowledges dispatch acceptance, not completion of application-side work.
// The application owns dispatch to its own event loop and any later state changes.
type ApplicationCommand = promptlang.ApplicationDefinition

// NewWithApplicationCommands validates and copies an immutable registration batch
// before starting the interpreter. Invalid names, collisions and nil callbacks
// return an error without starting workers or changing the Session. New remains
// available for callers without application commands.
func NewWithApplicationCommands(ctx context.Context, sess SessionController, cwd string, commands []ApplicationCommand, opts ...Option) (*Interpreter, error) {
	definitions := promptlang.NewRegistry()
	for _, command := range commands {
		if err := definitions.RegisterApplication(command); err != nil {
			return nil, fmt.Errorf("register application command: %w", err)
		}
	}
	return newInterpreter(ctx, sess, cwd, definitions, opts...), nil
}

type applicationInvocation struct {
	run   func() error
	queue func(func())
}

func (*applicationInvocation) Staging() *runtime.Staging { return nil }
func (*applicationInvocation) Close()                    {}
func (*applicationInvocation) ReadyToAcknowledge() bool  { return false }
func (i *applicationInvocation) Go(complete func(runtime.CommandResult)) error {
	i.queue(func() {
		result := runtime.CommandResult{}
		if err := i.run(); err != nil {
			result.Err = fmt.Errorf("dispatch application command: %w", err)
		}
		complete(result) // Callback only queues; the interpreter accepts and settles.
	})
	return nil
}
