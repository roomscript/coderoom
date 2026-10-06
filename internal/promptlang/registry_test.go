package promptlang_test

import (
	"testing"

	"github.com/roomscript/coderoom/internal/promptlang"
)

func TestRegistry_defineAndResolve(t *testing.T) {
	registry := promptlang.NewRegistry()
	definition := promptlang.CommandDefinition{
		Name: "tests",
		Body: promptlang.Shell{Program: "go test ./..."},
	}
	if err := registry.Define(definition); err != nil {
		t.Fatalf("Define: unexpected error: %v", err)
	}

	got, err := registry.Resolve(promptlang.CommandInvocation{Name: "tests"})
	if err != nil {
		t.Fatalf("Resolve: unexpected error: %v", err)
	}
	if got != definition.Body {
		t.Errorf("Resolve = %#v, want %#v", got, definition.Body)
	}
}

func TestRegistry_rejectsInvalidDefinitions(t *testing.T) {
	tests := []promptlang.CommandDefinition{
		{Name: ""},
		{Name: "1test"},
		{Name: "help"},
		{Name: "tests"},
	}
	registry := promptlang.NewRegistry()
	if err := registry.Define(promptlang.CommandDefinition{Name: "tests"}); err != nil {
		t.Fatalf("seed definition: %v", err)
	}
	for _, definition := range tests {
		t.Run(definition.Name, func(t *testing.T) {
			if err := registry.Define(definition); err == nil {
				t.Fatal("Define: expected error")
			}
		})
	}
}

func TestRegistry_rejectsUndefinedInvocation(t *testing.T) {
	registry := promptlang.NewRegistry()
	if _, err := registry.Resolve(promptlang.CommandInvocation{Name: "tests"}); err == nil {
		t.Fatal("Resolve: expected error")
	}
}
