package std

import (
	"errors"
	"fmt"

	"github.com/roomscript/coderoom/internal/interpreter/runtime"
	"github.com/roomscript/coderoom/internal/promptlang"
	"github.com/roomscript/coderoom/internal/room"
)

// DefineCommand stores shell-backed definitions without evaluating their bodies.
//
// User documentation: ../../../docs/man/def.md
// Saved command invocation: ../../../docs/man/user-command.md
type DefineCommand struct{}

var _ runtime.Command = DefineCommand{}

// Name identifies the command in the catalog.
func (DefineCommand) Name() string { return "def" }

// Usage supplies the command's help example.
func (DefineCommand) Usage() string { return "/def <name> /shell <program>" }

// Description supplies the command's help description.
func (DefineCommand) Description() string { return "define a shell-backed command" }

// Statement identifies the accepted parsed statement.
func (DefineCommand) Statement() promptlang.Statement { return promptlang.UserDefinition{} }

// Prepare captures command input and its required capabilities.
func (DefineCommand) Prepare(parsed promptlang.ParsedStatement, ctx runtime.Context) (runtime.Invocation, error) {
	definition, ok := parsed.Value.(promptlang.UserDefinition)
	if !ok {
		return nil, errors.New("def requires a definition statement")
	}
	if ctx.NamedCommands == nil {
		return nil, errors.New("def requires user command storage")
	}
	return &definitionInvocation{commands: ctx.NamedCommands, definition: definition}, nil
}

type definitionInvocation struct {
	commands   runtime.NamedCommandRegistry
	definition promptlang.UserDefinition
}

func (*definitionInvocation) Staging() *runtime.Staging { return nil }
func (i *definitionInvocation) Go(complete func(runtime.CommandResult)) error {
	if err := i.commands.Define(i.definition); err != nil {
		return fmt.Errorf("define command: %w", err)
	}
	complete(runtime.CommandResult{Records: []room.Record{{Kind: room.KindSystem, Text: "[defined] /" + i.definition.Name.Value}}})
	return nil
}
func (*definitionInvocation) Close() {}

// UserCommand resolves a named definition and uses the ordinary shell invocation.
type UserCommand struct{}

var _ runtime.Command = UserCommand{}

// Name identifies the command in the catalog.
func (UserCommand) Name() string { return "user-command" }

// Usage supplies the command's help example.
func (UserCommand) Usage() string { return "/<name>" }

// Description supplies the command's help description.
func (UserCommand) Description() string { return "invoke a defined command" }

// Statement identifies the accepted parsed statement.
func (UserCommand) Statement() promptlang.Statement { return promptlang.UserCommand{} }

// Prepare captures command input and its required capabilities.
func (c UserCommand) Prepare(parsed promptlang.ParsedStatement, ctx runtime.Context) (runtime.Invocation, error) {
	statement, ok := parsed.Value.(promptlang.UserCommand)
	if !ok {
		return nil, errors.New("named command requires a user command statement")
	}
	if ctx.NamedCommands == nil {
		return nil, errors.New("named command requires user command resolution")
	}
	definition, err := ctx.NamedCommands.Resolve(statement)
	if err != nil {
		return nil, fmt.Errorf("resolve command: %w", err)
	}
	return c.PrepareResolved(parsed, definition, ctx)
}

// PrepareResolved prepares a supplied definition without consulting the registry.
// The interpreter uses it after resolving a named invocation and selecting execution.
func (UserCommand) PrepareResolved(parsed promptlang.ParsedStatement, definition promptlang.Definition, ctx runtime.Context) (runtime.Invocation, error) {
	statement, ok := parsed.Value.(promptlang.UserCommand)
	if !ok {
		return nil, errors.New("named command requires a user command statement")
	}
	body, err := promptlang.RequireShell(definition)
	if err != nil {
		return nil, fmt.Errorf("prepare named shell: %w", err)
	}
	if ctx.Shell == nil {
		return nil, errors.New("named command requires shell execution")
	}
	return &shellInvocation{launcher: ctx.Shell, command: body.Value.Program.Value, displayCommand: "/" + statement.Name.Value, cwd: ctx.Shell.Cwd()}, nil
}
