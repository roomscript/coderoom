package interpreter

import "github.com/roomscript/coderoom/internal/interpreter/monolith"

// ShellRunner executes one shell program.
type ShellRunner = monolith.ShellRunner

// ShellRunnerFunc adapts a function to ShellRunner.
type ShellRunnerFunc = monolith.ShellRunnerFunc

// WithShellRunner replaces local shell execution, primarily for tests.
func WithShellRunner(runner ShellRunner) Option {
	return func(i *Interpreter) { monolith.WithShellRunner(runner)(i.fallback) }
}
