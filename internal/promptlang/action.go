// Package promptlang defines and parses the user-input language understood by
// coderoom. It is independent of the UI and session execution layers.
package promptlang

import (
	"fmt"

	"github.com/roomscript/coderoom/internal/policy"
)

// Statement is a sealed interface representing the content of a user input line.
// Only types defined in this package can implement it.
type Statement interface {
	isStatement()
}

// Invite invites a new agent into the session.
type Invite struct {
	Alias Located[string]
}

// Remove stops and removes an agent from the session.
type Remove struct {
	Alias Located[string]
}

// Cancel requests an agent to interrupt its current work.
type Cancel struct {
	Alias Located[string]
}

// Handoff transfers one agent's latest completed output to another.
type Handoff struct {
	FromAlias Located[string]
	ToAlias   Located[string]
}

// Send sends a message to one agent in the shared room (@alias text).
type Send struct {
	Alias Located[string]
	Text  Located[string]
}

// Broadcast sends a message to all agents.
type Broadcast struct {
	Text Located[string]
}

// PolicyEnable enables a room-local runtime policy.
type PolicyEnable struct {
	Name Located[policy.Name]
}

// Shell executes a shell program in the coderoom workspace.
type Shell struct {
	Program Located[string]
}

// CommandDefinition associates a command name with an unevaluated shell expression.
type CommandDefinition struct {
	Name Located[string]
	Body Located[Shell]
}

// CommandInvocation calls a user-defined command by name.
type CommandInvocation struct {
	Name Located[string]
}

// Loop repeatedly prompts a participant until a command succeeds or the turn
// bound is reached.
type Loop struct {
	Participant Located[string]
	Prompt      Located[string]
	Condition   Located[string]
	MaxTurns    Located[int]
}

// Who displays the current participants.
type Who struct{}

// Help displays available commands.
type Help struct{}

// Quit exits the session.
type Quit struct{}

// DebugView prints a short viewport debug dump (development aid).
type DebugView struct{}

// DebugRows toggles viewport row-number overlay.
type DebugRows struct{}

// UnknownCommandError is returned for an invalid user-command invocation.
type UnknownCommandError struct {
	Cmd string
}

func (e UnknownCommandError) Error() string {
	if e.Cmd == "" {
		return "unknown command"
	}
	return fmt.Sprintf("unknown command: %s", e.Cmd)
}

func (Invite) isStatement()            {}
func (Remove) isStatement()            {}
func (Cancel) isStatement()            {}
func (Handoff) isStatement()           {}
func (Send) isStatement()              {}
func (Broadcast) isStatement()         {}
func (PolicyEnable) isStatement()      {}
func (Shell) isStatement()             {}
func (CommandDefinition) isStatement() {}
func (CommandInvocation) isStatement() {}
func (Loop) isStatement()              {}
func (Who) isStatement()               {}
func (Help) isStatement()              {}
func (Quit) isStatement()              {}
func (DebugView) isStatement()         {}
func (DebugRows) isStatement()         {}
