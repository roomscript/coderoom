package interpreter

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"

	"github.com/trigosec/coderoom/internal/promptlang"
)

func TestNativeCommandDefinitions_coverParserBuiltins(t *testing.T) {
	// Read parser recognition independently of the interpreter catalog so adding
	// a parser built-in without registering dispatch cannot silently pass.
	file, err := parser.ParseFile(token.NewFileSet(), "../promptlang/parse.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	names := readParserBuiltinNames(file)
	if len(names) == 0 {
		t.Fatal("no parser built-ins found")
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

func readParserBuiltinNames(file *ast.File) map[string]bool {
	names := map[string]bool{}
	for _, declaration := range file.Decls {
		switch declaration := declaration.(type) {
		case *ast.GenDecl:
			collectNoArgBuiltinNames(declaration, names)
		case *ast.FuncDecl:
			if declaration.Name.Name == "parseSlash" {
				collectSlashBuiltinNames(declaration, names)
			}
		}
	}
	return names
}

func collectNoArgBuiltinNames(declaration *ast.GenDecl, names map[string]bool) {
	for _, specification := range declaration.Specs {
		value, ok := specification.(*ast.ValueSpec)
		if !ok || len(value.Names) != 1 || value.Names[0].Name != "noArgCommands" {
			continue
		}
		ast.Inspect(value, func(node ast.Node) bool {
			collectBuiltinName(node, names)
			return true
		})
	}
}

func collectSlashBuiltinNames(declaration *ast.FuncDecl, names map[string]bool) {
	ast.Inspect(declaration.Body, func(node ast.Node) bool {
		clause, ok := node.(*ast.CaseClause)
		if ok {
			for _, expression := range clause.List {
				collectBuiltinName(expression, names)
			}
		}
		return true
	})
}

func collectBuiltinName(node ast.Node, names map[string]bool) {
	literal, ok := node.(*ast.BasicLit)
	if !ok || literal.Kind != token.STRING {
		return
	}
	value, err := strconv.Unquote(literal.Value)
	if err == nil && strings.HasPrefix(value, "/") {
		names[strings.TrimPrefix(value, "/")] = true
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
	if !definition.matches(statement) {
		t.Fatalf("help example parsed as %T, outside its definition", statement)
	}
	matches := 0
	for _, candidate := range nativeCommandDefinitions {
		if candidate.matches(statement) {
			matches++
		}
	}
	if matches != 1 {
		t.Fatalf("matching definitions = %d, want 1", matches)
	}
	return statement
}

func assertNativeHelpDispatch(t *testing.T, raw string, statement promptlang.Statement) {
	t.Helper()
	model := newInterpreterModel()
	t.Cleanup(model.Close)
	if err := model.commands.Define(promptlang.CommandDefinition{Name: "check", Body: promptlang.Shell{Program: "true"}}); err != nil {
		t.Fatal(err)
	}
	sequence, handled := model.submitCommand(raw, statement)
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
			if _, handled := model.submitCommand(raw, statement); handled {
				t.Fatal("UI-only command dispatched natively")
			}
			for _, definition := range nativeCommandDefinitions {
				if definition.matches(statement) && (definition.submit != nil || len(definition.help) != 0) {
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
