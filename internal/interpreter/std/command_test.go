package std_test

import (
	"testing"

	"github.com/roomscript/coderoom/internal/interpreter/runtime"
	"github.com/roomscript/coderoom/internal/interpreter/std"
	"github.com/roomscript/coderoom/internal/promptlang"
)

func TestCommands_PrepareRejectsIncompatibleStatements(t *testing.T) {
	tests := []struct {
		name      string
		command   runtime.Command
		statement promptlang.Statement
	}{
		{name: "definition with shell", command: std.DefineCommand{}, statement: promptlang.Shell{}},
		{name: "named command with shell", command: std.UserCommand{}, statement: promptlang.Shell{}},
		{name: "definition without registry", command: std.DefineCommand{}, statement: promptlang.UserDefinition{}},
		{name: "named command without registry", command: std.UserCommand{}, statement: promptlang.UserCommand{}},
		{name: "who with shell", command: std.WhoCommand{}, statement: promptlang.Shell{}},
		{name: "shell with who", command: std.ShellCommand{}, statement: promptlang.Who{}},
		{name: "who without input", command: std.WhoCommand{}},
		{name: "shell without input", command: std.ShellCommand{}},
		{name: "shell with user command", command: std.ShellCommand{}, statement: promptlang.UserCommand{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := runtime.Context{Participants: participantReader{}, Shell: &shellLauncher{}}
			invocation, err := tt.command.Prepare(promptlang.ParsedStatement{Located: promptlang.Located[promptlang.Statement]{Value: tt.statement}}, ctx)
			if err == nil || invocation != nil {
				t.Fatal("expected preparation failure for incompatible input")
			}
		})
	}
}
