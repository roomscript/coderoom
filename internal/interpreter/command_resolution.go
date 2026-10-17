package interpreter

import (
	"fmt"

	"github.com/roomscript/coderoom/internal/interpreter/runtime"
	"github.com/roomscript/coderoom/internal/interpreter/std"
	"github.com/roomscript/coderoom/internal/promptlang"
)

// Select execution from one name resolution. Retain errors until normal admission
// and preparation so staged-input priority and unknown-command reporting stay put.
func (i *Interpreter) selectCommand(statement promptlang.ParsedStatement) runtime.Command {
	command, ok := i.commands.Lookup(statement)
	if !ok {
		return nil
	}
	if _, named := command.(std.UserCommand); !named {
		return command
	}
	invocation := statement.Value.(promptlang.UserCommand)
	definition, err := i.namedCommands.Resolve(invocation)
	return resolvedNamedCommand{definition: definition, err: err, queue: i.fallback.QueuePublication}
}

// resolvedNamedCommand carries the resolved target through admission and prepares
// it without another lookup. It reuses the named command's registry metadata.
type resolvedNamedCommand struct {
	std.UserCommand
	definition promptlang.Definition
	err        error
	queue      func(func())
}

func (c resolvedNamedCommand) Prepare(parsed promptlang.ParsedStatement, ctx runtime.Context) (runtime.Invocation, error) {
	if c.err != nil {
		return nil, c.err
	}
	if application, ok := c.definition.(promptlang.ApplicationDefinition); ok {
		return &applicationInvocation{run: application.Run, queue: c.queue}, nil
	}
	invocation, err := c.PrepareResolved(parsed, c.definition, ctx)
	if err != nil {
		return invocation, fmt.Errorf("prepare resolved command: %w", err)
	}
	return invocation, nil
}
