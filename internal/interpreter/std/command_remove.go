package std

import (
	"errors"

	"github.com/roomscript/coderoom/internal/interpreter/runtime"
	"github.com/roomscript/coderoom/internal/promptlang"
	"github.com/roomscript/coderoom/internal/session"
)

// RemoveCommand captures one participant lifecycle request for Session execution.
type RemoveCommand struct{}

var _ runtime.Command = RemoveCommand{}

// Name identifies the command in the catalog.
func (RemoveCommand) Name() string { return "remove" }

// Usage supplies the command's help example.
func (RemoveCommand) Usage() string { return "/remove <alias>" }

// Description supplies the command's help description.
func (RemoveCommand) Description() string { return "remove an agent" }

// Statement identifies the accepted parsed statement.
func (RemoveCommand) Statement() promptlang.Statement { return promptlang.Remove{} }

// Prepare captures input without changing participant state.
func (RemoveCommand) Prepare(parsed promptlang.ParsedStatement, ctx runtime.Context) (runtime.Invocation, error) {
	statement, ok := parsed.Value.(promptlang.Remove)
	if !ok {
		return nil, errors.New("remove requires a remove statement")
	}
	if ctx.Session == nil {
		return nil, errors.New("remove requires Session execution")
	}
	command := session.RemoveCommand{Alias: resolveCommandParticipant(ctx.Session, statement.Alias.Value)}
	return &unroutedInvocation{session: ctx.Session, command: command}, nil
}
