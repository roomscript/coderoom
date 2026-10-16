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

// Interpreter executes registered commands and owns their lifecycles, delegating
// other input to the monolith. Both paths share its serialized effect/publication
// lane during migration.
type Interpreter struct {
	fallback *monolith.Interpreter
	commands *runtime.Registry
	// Accessed only on the shared serialized queue, including shutdown.
	nextInvocationID uint64
	invocations      map[uint64]pendingInvocation
	stage            *stagedInvocation
}

// New starts one interpreter runtime backed by sess. Options are applied through
// the facade before the fallback starts, preserving initial observer publication.
func New(ctx context.Context, sess SessionController, cwd string, opts ...Option) *Interpreter {
	i := &Interpreter{commands: newCommandRegistry()}
	i.fallback = monolith.New(ctx, sess, cwd, func(fallback *monolith.Interpreter) {
		i.fallback = fallback
		monolith.WithShutdown(i.closeInvocations)(fallback)
		monolith.WithStageSnapshot(i.stageSnapshot)(fallback)
		for _, option := range opts {
			option(i)
		}
	})
	return i
}

func newCommandRegistry() *runtime.Registry {
	registry := runtime.NewRegistry()
	if err := registry.Register(std.WhoCommand{}); err != nil {
		panic(err)
	}
	if err := registry.Register(std.ShellCommand{}); err != nil {
		panic(err)
	}
	if err := registry.Register(std.SendCommand{}); err != nil {
		panic(err)
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
