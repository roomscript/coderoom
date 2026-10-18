// Package interpreter is the public entry point for coderoom's application layer.
package interpreter

import (
	"context"

	"github.com/roomscript/coderoom/internal/interpreter/monolith"
	"github.com/roomscript/coderoom/internal/interpreter/runtime"
	"github.com/roomscript/coderoom/internal/interpreter/std"
	"github.com/roomscript/coderoom/internal/promptlang"
)

// Option configures an Interpreter before execution and publication start.
type Option func(*Interpreter)

// Interpreter owns input dispatch, command lifecycles and staging. The monolith
// temporarily supplies the serialized execution/publication infrastructure.
type Interpreter struct {
	fallback      *monolith.Interpreter
	commands      *runtime.Registry
	namedCommands *promptlang.Registry
	// Accessed only on the shared serialized queue, including shutdown.
	nextInvocationID uint64
	invocations      map[uint64]pendingInvocation
	stage            *stagedInvocation
}

// New starts one interpreter runtime backed by sess. Options are applied through
// the facade before the fallback starts, preserving initial observer publication.
func New(ctx context.Context, sess SessionController, cwd string, opts ...Option) *Interpreter {
	return newInterpreter(ctx, sess, cwd, promptlang.NewRegistry(), opts...)
}

func newInterpreter(ctx context.Context, sess SessionController, cwd string, definitions *promptlang.Registry, opts ...Option) *Interpreter {
	i := &Interpreter{namedCommands: definitions}
	i.commands = newCommandRegistry(i.readHelp)
	i.fallback = monolith.New(ctx, sess, cwd, func(fallback *monolith.Interpreter) {
		i.fallback = fallback
		monolith.WithNamedCommands(i.namedCommands)(fallback)
		monolith.WithShutdown(i.closeInvocations)(fallback)
		monolith.WithStageSnapshot(i.stageSnapshot)(fallback)
		monolith.WithCompletedExecution(i.processCompletedExecutions)(fallback)
		for _, option := range opts {
			option(i)
		}
	})
	return i
}

func newCommandRegistry(readHelp func() HelpListed) *runtime.Registry {
	registry := runtime.NewRegistry()
	for _, command := range []runtime.Command{
		std.PolicyCommand{}, std.InviteCommand{}, std.RemoveCommand{}, std.CancelCommand{},
		std.HandoffCommand{}, std.ShellCommand{}, std.DefineCommand{}, std.UserCommand{}, std.LoopCommand{},
		std.WhoCommand{}, std.HelpCommand{ReadHelp: readHelp}, std.SendCommand{}, std.BroadcastCommand{},
	} {
		if err := registry.Register(command); err != nil {
			panic(err)
		}
	}
	return registry
}

// ResolveCommand returns a room-scoped command body, or ErrClosed after shutdown.
func (i *Interpreter) ResolveCommand(invocation promptlang.UserCommand) (promptlang.Shell, error) {
	return i.fallback.ResolveCommand(invocation) //nolint:wrapcheck // Preserve existing facade error identity.
}

// Snapshot returns a detached view of current application state.
func (i *Interpreter) Snapshot() Snapshot { return i.fallback.Snapshot() }

// Close stops the runtime and its owned background work.
func (i *Interpreter) Close() { i.fallback.Close() }
