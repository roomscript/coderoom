package promptlang

import "fmt"

// Registry stores command definitions for one running room.
type Registry struct {
	definitions map[string]CommandDefinition
}

// NewRegistry creates an empty command registry.
func NewRegistry() *Registry {
	return &Registry{definitions: make(map[string]CommandDefinition)}
}

// Define stores a command definition without evaluating its body.
func (r *Registry) Define(definition CommandDefinition) error {
	if !isIdentifier(definition.Name.Value) {
		return diagnosticForError(DiagnosticInvalidIdentifier, definition.Name.Span, InvalidCommandNameError{Name: definition.Name.Value})
	}
	if isReservedCommand(definition.Name.Value) {
		return diagnosticForError(DiagnosticReservedCommand, definition.Name.Span, ReservedCommandNameError{Name: definition.Name.Value})
	}
	if _, exists := r.definitions[definition.Name.Value]; exists {
		return diagnosticForError(DiagnosticCommandExists, definition.Name.Span, CommandAlreadyDefinedError{Name: definition.Name.Value})
	}
	if r.definitions == nil {
		r.definitions = make(map[string]CommandDefinition)
	}
	r.definitions[definition.Name.Value] = definition
	return nil
}

// Resolve returns the unevaluated shell body for a command invocation.
func (r *Registry) Resolve(invocation CommandInvocation) (Located[Shell], error) {
	definition, exists := r.definitions[invocation.Name.Value]
	if !exists {
		return Located[Shell]{}, diagnosticForError(DiagnosticUndefinedCommand, invocation.Name.Span, UndefinedCommandError{Name: invocation.Name.Value})
	}
	return definition.Body, nil
}

// InvalidCommandNameError reports a name that is not a valid identifier.
type InvalidCommandNameError struct{ Name string }

func (e InvalidCommandNameError) Error() string {
	return fmt.Sprintf("invalid command name %q", e.Name)
}

// ReservedCommandNameError reports an attempted built-in redefinition.
type ReservedCommandNameError struct{ Name string }

func (e ReservedCommandNameError) Error() string {
	return fmt.Sprintf("command name %q is reserved", e.Name)
}

// CommandAlreadyDefinedError reports a duplicate definition.
type CommandAlreadyDefinedError struct{ Name string }

func (e CommandAlreadyDefinedError) Error() string {
	return fmt.Sprintf("command %q is already defined", e.Name)
}

// UndefinedCommandError reports an invocation without a matching definition.
type UndefinedCommandError struct{ Name string }

func (e UndefinedCommandError) Error() string {
	return fmt.Sprintf("command %q is not defined", e.Name)
}
