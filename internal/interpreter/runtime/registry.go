package runtime

import (
	"errors"
	"fmt"
	"reflect"
	"slices"

	"github.com/roomscript/coderoom/internal/promptlang"
)

// Registry selects commands by their accepted statement type.
// Assemble it before use; concurrent registration and lookup are unsupported.
// Its zero value is ready for registration.
type Registry struct {
	byStatement map[reflect.Type]Command
	entries     []Command
}

// NewRegistry creates an empty registry without installing any commands.
func NewRegistry() *Registry { return &Registry{} }

// Register adds a command, rejecting missing metadata and duplicate names or types.
func (r *Registry) Register(command Command) error {
	if command == nil {
		return errors.New("command registration requires a command")
	}
	name := command.Name()
	if name == "" {
		return errors.New("command registration requires a name")
	}
	statementType := reflect.TypeOf(command.Statement())
	if statementType == nil {
		return errors.New("command registration requires a statement")
	}
	if _, exists := r.byStatement[statementType]; exists {
		return fmt.Errorf("statement %v is already registered", statementType)
	}
	for _, registered := range r.entries {
		if registered.Name() == name {
			return fmt.Errorf("command %q is already registered", name)
		}
	}
	if r.byStatement == nil {
		r.byStatement = make(map[reflect.Type]Command)
	}
	r.byStatement[statementType] = command
	r.entries = append(r.entries, command)
	return nil
}

// Lookup selects the command accepting the parsed statement's concrete type.
func (r *Registry) Lookup(statement promptlang.ParsedStatement) (Command, bool) {
	command, ok := r.byStatement[reflect.TypeOf(statement.Value)]
	return command, ok
}

// Entries returns a detached list in registration order for help and inspection.
func (r *Registry) Entries() []Command { return slices.Clone(r.entries) }
