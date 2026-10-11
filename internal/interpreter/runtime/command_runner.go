package runtime

import (
	"fmt"

	"github.com/roomscript/coderoom/internal/promptlang"
)

// CommandRunner is the existing stateless bridge used by the interpreter.
// The InvocationLifecycle prototype is not wired into this path.
type CommandRunner struct{}

// Go returns preparation or launch failure. Completion may arrive immediately
// or later; launch acceptance and execution completion are separate milestones.
func (CommandRunner) Go(command Command, statement promptlang.ParsedStatement, ctx Context, complete func(CommandResult)) error {
	invocation, err := command.Prepare(statement, ctx)
	if err != nil {
		return fmt.Errorf("prepare command: %w", err)
	}
	if err := invocation.Go(complete); err != nil {
		return fmt.Errorf("launch command: %w", err)
	}
	return nil
}
