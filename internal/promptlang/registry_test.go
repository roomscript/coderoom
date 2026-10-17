package promptlang_test

import (
	"errors"
	"testing"

	"github.com/roomscript/coderoom/internal/promptlang"
)

func TestRegistry_defineAndResolve(t *testing.T) {
	registry := promptlang.NewRegistry()
	definition := promptlang.UserDefinition{
		Name: located("tests"),
		Body: located(promptlang.Shell{Program: located("go test ./...")}),
	}
	if err := registry.Define(definition); err != nil {
		t.Fatalf("Define: unexpected error: %v", err)
	}

	got, err := registry.Resolve(promptlang.UserCommand{Name: located("tests")})
	if err != nil {
		t.Fatalf("Resolve: unexpected error: %v", err)
	}
	if got != definition {
		t.Errorf("Resolve = %#v, want %#v", got, definition)
	}
}

func TestRegistry_rejectsInvalidDefinitions(t *testing.T) {
	tests := []promptlang.UserDefinition{
		{Name: located("")},
		{Name: located("1test")},
		{Name: located("help")},
		{Name: located("tests")},
	}
	registry := promptlang.NewRegistry()
	if err := registry.Define(promptlang.UserDefinition{Name: located("tests")}); err != nil {
		t.Fatalf("seed definition: %v", err)
	}
	for _, definition := range tests {
		t.Run(definition.Name.Value, func(t *testing.T) {
			if err := registry.Define(definition); err == nil {
				t.Fatal("Define: expected error")
			}
		})
	}
}

func TestRegistry_rejectsUndefinedInvocation(t *testing.T) {
	registry := promptlang.NewRegistry()
	if _, err := registry.Resolve(promptlang.UserCommand{Name: located("tests")}); err == nil {
		t.Fatal("Resolve: expected error")
	}
}

func TestRegistry_applicationSharesNamespace(t *testing.T) {
	registry := promptlang.NewRegistry()
	if err := registry.RegisterApplication(promptlang.ApplicationDefinition{Name: "inspect", Run: func() error { return nil }}); err != nil {
		t.Fatal(err)
	}
	invocation := promptlang.UserCommand{Name: located("inspect")}
	definition, err := registry.Resolve(invocation)
	if err != nil {
		t.Fatal(err)
	}
	application, ok := definition.(promptlang.ApplicationDefinition)
	if !ok || application.Name != "inspect" {
		t.Fatalf("definition=%#v", definition)
	}
	if err := registry.Define(promptlang.UserDefinition{Name: located("inspect")}); err == nil {
		t.Fatal("shell definition overwrote application")
	}
	_, err = promptlang.RequireShell(definition)
	var notShell promptlang.NotShellCommandError
	if !errors.As(err, &notShell) {
		t.Fatalf("expected non-shell definition error, got %v", err)
	}
}

func TestRegistry_applicationRegistrationValidation(t *testing.T) {
	for _, name := range []string{"", "1bad", "help", "inspect", "shell-name"} {
		t.Run(name, func(t *testing.T) {
			registry := promptlang.NewRegistry()
			if err := registry.RegisterApplication(promptlang.ApplicationDefinition{Name: "inspect", Run: func() error { return nil }}); err != nil {
				t.Fatal(err)
			}
			if err := registry.Define(promptlang.UserDefinition{Name: located("shell-name")}); err != nil {
				t.Fatal(err)
			}
			if err := registry.RegisterApplication(promptlang.ApplicationDefinition{Name: name, Run: func() error { return nil }}); err == nil {
				t.Fatal("invalid registration accepted")
			}
		})
	}
}

func TestRegistry_retainsCompleteApplicationDefinition(t *testing.T) {
	registry := promptlang.NewRegistry()
	calls := 0
	definition := promptlang.ApplicationDefinition{Name: "inspect", Description: "inspect application", Run: func() error { calls++; return nil }}
	if err := registry.RegisterApplication(definition); err != nil {
		t.Fatal(err)
	}
	definition.Description = "modified"
	definition.Run = nil
	resolved, err := registry.Resolve(promptlang.UserCommand{Name: located("inspect")})
	if err != nil {
		t.Fatal(err)
	}
	application := resolved.(promptlang.ApplicationDefinition)
	if application.Description != "inspect application" || calls != 0 {
		t.Fatal("registry lost metadata or invoked callback")
	}
	if err := application.Run(); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal("registry lost callback")
	}
}

func TestRegistry_rejectsApplicationWithoutCallback(t *testing.T) {
	registry := promptlang.NewRegistry()
	if err := registry.RegisterApplication(promptlang.ApplicationDefinition{Name: "inspect"}); err == nil {
		t.Fatal("missing callback accepted")
	}
	if _, err := registry.Resolve(promptlang.UserCommand{Name: located("inspect")}); err == nil {
		t.Fatal("invalid definition stored")
	}
}
