package runtime_test

import (
	"testing"

	"github.com/roomscript/coderoom/internal/interpreter/runtime"
	"github.com/roomscript/coderoom/internal/interpreter/std"
	"github.com/roomscript/coderoom/internal/promptlang"
)

func TestRegistry_Lookup(t *testing.T) {
	registry := runtime.NewRegistry()
	for _, command := range []runtime.Command{std.WhoCommand{}, std.ShellCommand{}} {
		if err := registry.Register(command); err != nil {
			t.Fatal(err)
		}
	}
	tests := []struct {
		input string
		name  string
		found bool
	}{
		{input: "/who", name: "who", found: true},
		{input: "/shell echo hello", name: "shell", found: true},
		{input: "/help"},
		{input: "/tests"},
		{input: "/def tests /shell true"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			statement, err := promptlang.Parse(tt.input)
			if err != nil {
				t.Fatal(err)
			}
			command, ok := registry.Lookup(statement)
			if ok != tt.found {
				t.Fatalf("found = %v, want %v", ok, tt.found)
			}
			if ok && command.Name() != tt.name {
				t.Fatalf("command = %q, want %q", command.Name(), tt.name)
			}
		})
	}
	if _, ok := registry.Lookup(promptlang.ParsedStatement{}); ok {
		t.Fatal("matched empty statement")
	}
}

type registryCommand struct {
	runtime.Command
	name      string
	statement promptlang.Statement
}

func (c registryCommand) Name() string                    { return c.name }
func (c registryCommand) Statement() promptlang.Statement { return c.statement }

func TestRegistry_RegisterRejectsInvalidCommands(t *testing.T) {
	tests := []struct {
		name    string
		command runtime.Command
	}{
		{name: "nil command"},
		{name: "missing name", command: registryCommand{Command: std.ShellCommand{}, statement: promptlang.Shell{}}},
		{name: "missing statement", command: registryCommand{Command: std.ShellCommand{}, name: "shell"}},
		{name: "duplicate name", command: registryCommand{Command: std.ShellCommand{}, name: "who", statement: promptlang.Shell{}}},
		{name: "duplicate type", command: registryCommand{Command: std.WhoCommand{}, name: "another", statement: promptlang.Who{}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var registry runtime.Registry
			if err := registry.Register(std.WhoCommand{}); err != nil {
				t.Fatal(err)
			}
			if err := registry.Register(tt.command); err == nil {
				t.Fatal("expected registration failure")
			}
			entries := registry.Entries()
			if len(entries) != 1 || entries[0].Name() != "who" {
				t.Fatalf("entries = %#v", entries)
			}
		})
	}
}

func TestRegistry_EntriesPreserveOrderAndAreDetached(t *testing.T) {
	registry := runtime.NewRegistry()
	for _, command := range []runtime.Command{std.WhoCommand{}, std.ShellCommand{}} {
		if err := registry.Register(command); err != nil {
			t.Fatal(err)
		}
	}
	entries := registry.Entries()
	if len(entries) != 2 || entries[0].Name() != "who" || entries[1].Name() != "shell" {
		t.Fatalf("entries = %#v, want registration order", entries)
	}
	entries[0] = std.ShellCommand{}
	command, ok := registry.Lookup(promptlang.ParsedStatement{Value: promptlang.Who{}})
	if !ok || command.Name() != "who" || registry.Entries()[0].Name() != "who" {
		t.Fatal("changing returned entries mutated the registry")
	}
}
