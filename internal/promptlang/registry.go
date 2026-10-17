package promptlang

import "fmt"

// Definition identifies a named command without owning its execution.
// The registry retains shell bodies and application callbacks as data.
type Definition interface{ commandDefinition() }

// ApplicationDefinition describes a command supplied by the embedding application.
// The registry stores Run without invoking it; execution belongs to the interpreter.
type ApplicationDefinition struct {
	Name        string
	Description string
	Run         func() error
}

func (UserDefinition) commandDefinition()        {}
func (ApplicationDefinition) commandDefinition() {}

// Registry stores command definitions for one running room.
type Registry struct {
	definitions map[string]Definition
}

// NewRegistry creates an empty command registry.
func NewRegistry() *Registry {
	return &Registry{definitions: make(map[string]Definition)}
}

// Define stores a command definition without evaluating its body.
func (r *Registry) Define(definition UserDefinition) error {
	return r.define(definition.Name, definition)
}

// RegisterApplication stores a complete application definition in the shared namespace.
func (r *Registry) RegisterApplication(definition ApplicationDefinition) error {
	if definition.Run == nil {
		return fmt.Errorf("application command %q requires a callback", definition.Name)
	}
	return r.define(Located[string]{Value: definition.Name}, definition)
}

func (r *Registry) define(name Located[string], definition Definition) error {
	if !isIdentifier(name.Value) {
		return diagnosticForError(DiagnosticInvalidIdentifier, name.Span, InvalidCommandNameError{Name: name.Value})
	}
	if isReservedCommand(name.Value) {
		return diagnosticForError(DiagnosticReservedCommand, name.Span, ReservedCommandNameError{Name: name.Value})
	}
	if _, exists := r.definitions[name.Value]; exists {
		return diagnosticForError(DiagnosticCommandExists, name.Span, CommandAlreadyDefinedError{Name: name.Value})
	}
	if r.definitions == nil {
		r.definitions = make(map[string]Definition)
	}
	r.definitions[name.Value] = definition
	return nil
}

// Resolve returns a shell or application definition without executing it.
func (r *Registry) Resolve(invocation UserCommand) (Definition, error) {
	definition, exists := r.definitions[invocation.Name.Value]
	if !exists {
		return nil, diagnosticForError(DiagnosticUndefinedCommand, invocation.Name.Span, UndefinedCommandError{Name: invocation.Name.Value})
	}
	return definition, nil
}

// RequireShell validates an already-resolved definition without another lookup.
func RequireShell(definition Definition) (Located[Shell], error) {
	switch definition := definition.(type) {
	case UserDefinition:
		return definition.Body, nil
	case ApplicationDefinition:
		return Located[Shell]{}, NotShellCommandError{Name: definition.Name}
	default:
		return Located[Shell]{}, fmt.Errorf("invalid command definition %T", definition)
	}
}

// NotShellCommandError reports an application command used where a shell is required.
type NotShellCommandError struct{ Name string }

func (e NotShellCommandError) Error() string {
	return fmt.Sprintf("command %q is not shell-backed", e.Name)
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
