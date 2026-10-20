package std

import (
	"errors"

	"github.com/roomscript/coderoom/internal/interpreter/runtime"
	"github.com/roomscript/coderoom/internal/promptlang"
	"github.com/roomscript/coderoom/internal/session"
)

// ModelCommand shows or changes a participant's model and effort through
// Session execution. Both forms complete on admission; their outcomes arrive
// as Session events, which the room renders.
type ModelCommand struct{}

var _ runtime.Command = ModelCommand{}

// Name identifies the command in the catalog.
func (ModelCommand) Name() string { return "model" }

// Usage supplies the command's help example.
func (ModelCommand) Usage() string { return "/model <alias> [<model> [<effort>]]" }

// Description supplies the command's help description.
func (ModelCommand) Description() string {
	return "show an agent's model, effort and available models, or change them for its current session"
}

// Statement identifies the accepted parsed statement.
func (ModelCommand) Statement() promptlang.Statement { return promptlang.Model{} }

// Prepare captures input without changing participant state.
func (ModelCommand) Prepare(parsed promptlang.ParsedStatement, ctx runtime.Context) (runtime.Invocation, error) {
	statement, ok := parsed.Value.(promptlang.Model)
	if !ok {
		return nil, errors.New("model requires a model statement")
	}
	if ctx.Session == nil {
		return nil, errors.New("model requires Session execution")
	}
	alias := resolveCommandParticipant(ctx.Session, statement.Alias.Value)
	var command session.Command = session.ListModelsCommand{Alias: alias}
	if statement.Model.Value != "" {
		command = session.ChangeModelCommand{Alias: alias, Model: statement.Model.Value, Effort: statement.Effort.Value}
	}
	return &unroutedInvocation{session: ctx.Session, command: command}, nil
}
