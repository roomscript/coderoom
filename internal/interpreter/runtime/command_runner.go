package runtime

import (
	"fmt"

	"github.com/roomscript/coderoom/internal/promptlang"
)

// CommandRunner prepares and launches commands without owning workers or
// publication. The caller supplies a completion callback that only enqueues work.
type CommandRunner struct{}

// Go returns preparation or launch failure. Completion may arrive immediately
// or later; launch acceptance and execution completion are separate milestones.
func (CommandRunner) Go(command Command, statement promptlang.ParsedStatement, ctx Context, complete func(Completion)) error {
	invocation, err := command.Prepare(statement, ctx)
	if err != nil {
		return fmt.Errorf("prepare command: %w", err)
	}
	if err := invocation.Go(complete); err != nil {
		return fmt.Errorf("launch command: %w", err)
	}
	return nil
}
