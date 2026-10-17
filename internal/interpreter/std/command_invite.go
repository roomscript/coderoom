package std

import (
	"errors"

	"github.com/roomscript/coderoom/internal/interpreter/runtime"
	"github.com/roomscript/coderoom/internal/promptlang"
	"github.com/roomscript/coderoom/internal/session"
)

// InviteCommand captures one participant lifecycle request for Session execution.
type InviteCommand struct{}

var _ runtime.Command = InviteCommand{}

// Name identifies the command in the catalog.
func (InviteCommand) Name() string { return "invite" }

// Usage supplies the command's help example.
func (InviteCommand) Usage() string { return "/invite <alias>" }

// Description supplies the command's help description.
func (InviteCommand) Description() string { return "start an agent" }

// Statement identifies the accepted parsed statement.
func (InviteCommand) Statement() promptlang.Statement { return promptlang.Invite{} }

// Prepare captures input without changing participant state.
func (InviteCommand) Prepare(parsed promptlang.ParsedStatement, ctx runtime.Context) (runtime.Invocation, error) {
	statement, ok := parsed.Value.(promptlang.Invite)
	if !ok {
		return nil, errors.New("invite requires an invite statement")
	}
	if ctx.Session == nil {
		return nil, errors.New("invite requires Session execution")
	}
	command := session.InviteCommand{Alias: statement.Alias.Value}
	return &unroutedInvocation{session: ctx.Session, command: command}, nil
}
