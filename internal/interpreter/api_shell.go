package interpreter

import (
	"context"

	"github.com/roomscript/coderoom/internal/shell"
)

// ShellRunner executes one shell program.
type ShellRunner interface {
	Run(context.Context, string, string) shell.Result
}

// ShellRunnerFunc adapts a function to ShellRunner.
type ShellRunnerFunc func(context.Context, string, string) shell.Result

// Run executes the adapted function.
func (f ShellRunnerFunc) Run(ctx context.Context, cwd, program string) shell.Result {
	return f(ctx, cwd, program)
}

// WithShellRunner replaces local shell execution, primarily for tests.
func WithShellRunner(runner ShellRunner) Option {
	return func(i *Interpreter) {
		if runner != nil {
			i.executor.setShellRunner(runner)
		}
	}
}
