package std_test

import (
	"errors"
	"testing"

	"github.com/roomscript/coderoom/internal/agent"
	"github.com/roomscript/coderoom/internal/interpreter/runtime"
	"github.com/roomscript/coderoom/internal/interpreter/std"
	"github.com/roomscript/coderoom/internal/room"
	"github.com/roomscript/coderoom/internal/shell"
)

type shellLauncher struct {
	cwd      string
	program  string
	complete func(shell.Result)
	err      error
}

func (l *shellLauncher) Cwd() string { return l.cwd }
func (l *shellLauncher) Go(program string, complete func(shell.Result)) error {
	if l.err != nil {
		return l.err
	}
	l.program, l.complete = program, complete
	return nil
}

func TestShellCommand_DelayedCompletion(t *testing.T) {
	failure := errors.New("execution failure")
	exitCode := 7
	tests := []struct {
		name   string
		result shell.Result
		output string
	}{
		{name: "success", result: shell.Result{Status: shell.StatusSuccess, Stdout: "hello"}, output: "status: success\nstdout:\nhello"},
		{name: "failure", result: shell.Result{Status: shell.StatusFailure, ExitCode: &exitCode, Stderr: "bad", Err: failure}, output: "status: failure\nstderr:\nbad\nerror:\nexecution failure"},
		{name: "cancelled", result: shell.Result{Status: shell.StatusCancelled}, output: "status: cancelled"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			launcher := &shellLauncher{cwd: "/workspace"}
			invocation, err := (std.ShellCommand{Command: "echo hello", DisplayCommand: "/greet"}).Prepare(runtime.Context{Shell: launcher})
			if err != nil {
				t.Fatal(err)
			}
			launcher.cwd = "/changed"
			results := make(chan runtime.Completion, 2)
			if err := invocation.Go(func(c runtime.Completion) { results <- c }); err != nil {
				t.Fatal(err)
			}
			if launcher.program != "echo hello" {
				t.Fatalf("program = %q", launcher.program)
			}
			select {
			case <-results:
				t.Fatal("completed before shell execution finished")
			default:
			}
			launcher.complete(tt.result)
			completion := <-results
			assertShellRecord(t, completion, tt.result, tt.output)
		})
	}
}

func assertShellRecord(t *testing.T, completion runtime.Completion, result shell.Result, output string) {
	t.Helper()
	if !errors.Is(completion.Err, result.Err) || len(completion.Records) != 1 {
		t.Fatalf("completion = %#v", completion)
	}
	record := completion.Records[0]
	command, ok := record.Msg.Content.(agent.Command)
	if record.Kind != room.KindCommand || record.Alias != "shell" || !ok {
		t.Fatalf("record = %#v", record)
	}
	if command.Command != "/greet" || command.Cwd != "/workspace" || command.Output != output || command.ExitCode != result.ExitCode {
		t.Fatalf("command = %#v", command)
	}
}

func TestShellCommand_LaunchFailure(t *testing.T) {
	failure := errors.New("launch failure")
	launcher := &shellLauncher{err: failure}
	err := (runtime.CommandRunner{}).Go(std.ShellCommand{Command: "true"}, runtime.Context{Shell: launcher}, func(runtime.Completion) {
		t.Fatal("launch failure must not report execution completion")
	})
	if !errors.Is(err, failure) {
		t.Fatalf("launch error = %v", err)
	}
}

func TestShellCommand_RequiresExecutionCapability(t *testing.T) {
	invocation, err := (std.ShellCommand{}).Prepare(runtime.Context{})
	if err == nil || invocation != nil {
		t.Fatal("expected preparation failure without shell execution")
	}
}
