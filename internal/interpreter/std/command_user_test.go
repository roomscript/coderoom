package std_test

import (
	"errors"
	"testing"

	"github.com/roomscript/coderoom/internal/interpreter/runtime"
	"github.com/roomscript/coderoom/internal/interpreter/std"
	"github.com/roomscript/coderoom/internal/promptlang"
	"github.com/roomscript/coderoom/internal/shell"
)

func TestUserCommand_definitionAndInvocation(t *testing.T) {
	commands := promptlang.NewRegistry()
	launcher := &shellLauncher{cwd: "/workspace"}
	ctx := runtime.Context{UserCommands: commands, Shell: launcher}
	definition, err := (std.DefineCommand{}).Prepare(parseSendStatement(t, "/def tests /shell go test ./..."), ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer definition.Close()
	var defined runtime.CommandResult
	if err := definition.Go(func(result runtime.CommandResult) { defined = result }); err != nil {
		t.Fatal(err)
	}
	if launcher.program != "" || len(defined.Records) != 1 || defined.Records[0].Text != "[defined] /tests" {
		t.Fatalf("definition=%#v, launch=%q", defined, launcher.program)
	}
	invocation, err := (std.UserCommand{}).Prepare(parseSendStatement(t, "/tests"), ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer invocation.Close()
	var results []runtime.CommandResult
	if err := invocation.Go(func(result runtime.CommandResult) { results = append(results, result) }); err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatal("named invocation did not launch the resolved program asynchronously")
	}
	result := shell.Result{Status: shell.StatusSuccess, Stdout: "pass"}
	launcher.complete(result)
	assertNamedCommandResult(t, launcher, results, result)
}

func TestUserCommand_registryErrors(t *testing.T) {
	for _, test := range []struct {
		name, input string
		command     runtime.Command
	}{
		{"reserved", "/def help /shell true", std.DefineCommand{}},
		{"duplicate", "/def tests /shell other", std.DefineCommand{}},
		{"undefined", "/missing", std.UserCommand{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			commands := promptlang.NewRegistry()
			seed := parseSendStatement(t, "/def tests /shell original").Value.(promptlang.UserDefinition)
			if err := commands.Define(seed); err != nil {
				t.Fatal(err)
			}
			invocation, err := test.command.Prepare(parseSendStatement(t, test.input), runtime.Context{UserCommands: commands, Shell: &shellLauncher{}})
			if err == nil {
				defer invocation.Close()
				err = invocation.Go(func(runtime.CommandResult) { t.Error("failed definition completed") })
			}
			var diagnostic *promptlang.Diagnostic
			if !errors.As(err, &diagnostic) {
				t.Fatalf("missing registry diagnostic: %v", err)
			}
			body, err := commands.Resolve(promptlang.UserCommand{Name: seed.Name})
			if err != nil || body != seed.Body {
				t.Fatal("failed operation changed stored definition")
			}
		})
	}
}

func assertNamedCommandResult(t *testing.T, launcher *shellLauncher, results []runtime.CommandResult, result shell.Result) {
	t.Helper()
	if len(results) != 1 {
		t.Fatalf("results=%#v", results)
	}
	if launcher.program != "go test ./..." {
		t.Fatalf("program=%q", launcher.program)
	}
	assertShellRecord(t, results[0], result, "/tests", "status: success\nstdout:\npass")
}
