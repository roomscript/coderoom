package std

import (
	"errors"

	"github.com/roomscript/coderoom/internal/interpreter/runtime"
	"github.com/roomscript/coderoom/internal/promptlang"
	"github.com/roomscript/coderoom/internal/session"
)

// CancelCommand captures one participant lifecycle request for Session execution.
type CancelCommand struct{}

var _ runtime.Command = CancelCommand{}

// Name identifies the command in the catalog.
func (CancelCommand) Name() string { return "cancel" }

// Usage supplies the command's help example.
func (CancelCommand) Usage() string { return "/cancel <alias>" }

// Description supplies the command's help description.
func (CancelCommand) Description() string { return "interrupt an agent's current turn" }

// Statement identifies the accepted parsed statement.
func (CancelCommand) Statement() promptlang.Statement { return promptlang.Cancel{} }

// Prepare captures input without changing participant state.
func (CancelCommand) Prepare(parsed promptlang.ParsedStatement, ctx runtime.Context) (runtime.Invocation, error) {
	statement, ok := parsed.Value.(promptlang.Cancel)
	if !ok {
		return nil, errors.New("cancel requires a cancel statement")
	}
	if ctx.Session == nil {
		return nil, errors.New("cancel requires Session execution")
	}
	command := session.CancelCommand{Alias: resolveCommandParticipant(ctx.Session, statement.Alias.Value)}
	return &unroutedInvocation{session: ctx.Session, command: command}, nil
}
