package runtime_test

import (
	"github.com/roomscript/coderoom/internal/interpreter/runtime"
	"github.com/roomscript/coderoom/internal/promptlang"
)

type command struct {
	invocation *invocation
	err        error
}

func (*command) Statement() promptlang.Statement { return promptlang.Who{} }
func (*command) Name() string                    { return "test" }
func (*command) Usage() string                   { return "/test" }
func (*command) Description() string             { return "test command" }
func (c *command) Prepare(promptlang.ParsedStatement, runtime.Context) (runtime.Invocation, error) {
	return c.invocation, c.err
}

type invocation struct {
	complete  func(runtime.CommandResult)
	err       error
	launches  int
	closes    int
	staging   *runtime.Staging
	immediate bool
	result    runtime.CommandResult
}

func (i *invocation) Staging() *runtime.Staging { return i.staging }

func (i *invocation) Go(complete func(runtime.CommandResult)) error {
	i.launches++
	i.complete = complete
	if i.immediate {
		complete(i.result)
	}
	return i.err
}

func (i *invocation) Close() { i.closes++ }
