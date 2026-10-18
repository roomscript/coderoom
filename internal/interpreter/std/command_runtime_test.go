package std_test

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
