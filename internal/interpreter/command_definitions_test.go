package interpreter

import (
	"strings"
	"testing"

	"github.com/roomscript/coderoom/internal/promptlang"
)

func TestNativeCommandDefinitions_coverParserBuiltins(t *testing.T) {
	// Query syntax independently of the execution catalog so a recognized form
	// cannot silently lack a runtime handler.
	names := map[string]bool{}
	for _, name := range promptlang.BuiltinNames() {
		names[name] = true
	}
	registered := map[string]bool{}
	for _, definition := range nativeCommandDefinitions {
		if definition.name == "" {
			continue
		}
		if registered[definition.name] {
			t.Errorf("duplicate definition: %s", definition.name)
		}
		registered[definition.name] = true
	}
	for name := range names {
		if !registered[name] {
			t.Errorf("parser built-in %s has no definition", name)
		}
	}
	for name := range registered {
		if !names[name] {
			t.Errorf("definition %s has no parser built-in", name)
		}
	}
}

func TestNativeCommandDefinitions_helpRoutesToNativeHandlers(t *testing.T) {
	replacements := strings.NewReplacer("<alias>", "ada", "<from>", "ada", "<to>", "turing", "<program>", "true", "<name>", "check", "<prompt>", "review", "<turns>", "1", "<text>", "hello")
	for _, definition := range nativeCommandDefinitions {
		for _, entry := range definition.help {
			t.Run(entry.Usage, func(t *testing.T) {
				raw := replacements.Replace(entry.Usage)
				statement := assertHelpExampleDefinition(t, definition, entry, raw)
				assertNativeHelpDispatch(t, raw, statement)

			})
		}
	}
}

func assertHelpExampleDefinition(t *testing.T, definition nativeCommandDefinition, entry HelpEntry, raw string) promptlang.Statement {
	t.Helper()
	if entry.Description == "" {
		t.Fatal("missing description")
	}
	statement, err := promptlang.Parse(raw)
	if err != nil {
		t.Fatalf("parse help example: %v", err)
	}
	if !definition.matches(statement.Value) {
		t.Fatalf("help example parsed as %T, outside its definition", statement)
	}
	matches := 0
	for _, candidate := range nativeCommandDefinitions {
		if candidate.matches(statement.Value) {
			matches++
		}
	}
	if matches != 1 {
		t.Fatalf("matching definitions = %d, want 1", matches)
	}
	return statement.Value
}

func assertNativeHelpDispatch(t *testing.T, raw string, statement promptlang.Statement) {
	t.Helper()
	model := newInterpreterModel()
	t.Cleanup(model.Close)
	if err := model.commands.Define(promptlang.CommandDefinition{Name: located("check"), Body: located(promptlang.Shell{Program: located("true")})}); err != nil {
		t.Fatal(err)
	}
	sequence, handled := model.prepareCommand(raw, statement)
	if !handled || len(sequence) == 0 {
		t.Fatal("help example did not reach a native handler")
	}
	for _, instruction := range sequence {
		if published, ok := instruction.(publishEventInstruction); ok {
			if _, unknown := published.event.(UnknownCommand); unknown {
				t.Fatal("help example routed to unknown command")
			}
		}
	}
}

func TestNativeCommandDefinitions_debugCommandsRemainUIOnly(t *testing.T) {
	for _, raw := range []string{"/debugview", "/debugrows"} {
		t.Run(raw, func(t *testing.T) {
			statement, err := promptlang.Parse(raw)
			if err != nil {
				t.Fatal(err)
			}
			model := newInterpreterModel()
			t.Cleanup(model.Close)
			if _, handled := model.prepareCommand(raw, statement.Value); handled {
				t.Fatal("UI-only command dispatched natively")
			}
			for _, definition := range nativeCommandDefinitions {
				if definition.matches(statement.Value) && (definition.submit != nil || len(definition.help) != 0) {
					t.Fatal("UI-only definition exposes native dispatch or help")
				}
			}
		})
	}
}

func TestNativeCommandDefinitions_requireNativeHelpAndHandlers(t *testing.T) {
	for _, definition := range nativeCommandDefinitions {
		if definition.name == "debugview" || definition.name == "debugrows" {
			continue
		}
		if definition.submit == nil {
			t.Errorf("native definition %q has no handler", definition.name)
		}
		if len(definition.help) == 0 {
			t.Errorf("native definition %q has no help", definition.name)
		}
	}
}

func TestNativeCommandDefinitions_coverMessagesAndInvocation(t *testing.T) {
	tests := []struct {
		name      string
		statement promptlang.Statement
		usage     string
		message   bool
	}{
		{name: "direct send", statement: promptlang.Send{}, usage: "@<alias> <text>", message: true},
		{name: "broadcast", statement: promptlang.Broadcast{}, usage: "<text>", message: true},
		{name: "defined command", statement: promptlang.CommandInvocation{}, usage: "/<name>"},
	}
	listing := helpListing()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matches := 0
			for _, definition := range nativeCommandDefinitions {
				if !definition.matches(tt.statement) {
					continue
				}
				matches++
				if definition.submit == nil || definition.message != tt.message {
					t.Error("definition has no handler or wrong help category")
				}
			}
			if matches != 1 {
				t.Fatalf("matching definitions = %d, want 1", matches)
			}
			entries := listing.Commands
			if tt.message {
				entries = listing.Messages
			}
			assertHelpUsage(t, entries, tt.usage)
		})
	}
}

func TestHelpListing_coversPolicyVariants(t *testing.T) {
	listing := helpListing()
	for _, usage := range []string{"/policy enable send-notices", "/policy enable echo-invites"} {
		t.Run(usage, func(t *testing.T) {
			assertHelpUsage(t, listing.Commands, usage)
		})
	}
}

func assertHelpUsage(t *testing.T, entries []HelpEntry, usage string) {
	t.Helper()
	count := 0
	for _, entry := range entries {
		if entry.Usage == usage {
			count++
		}
	}
	if count != 1 {
		t.Errorf("help usage %q occurs %d times, want 1", usage, count)
	}
}
