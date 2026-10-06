package ui

import (
	"errors"
	"strings"
	"testing"

	"github.com/roomscript/coderoom/internal/agent"
	"github.com/roomscript/coderoom/internal/interpreter"
	roomstate "github.com/roomscript/coderoom/internal/room"
	"github.com/roomscript/coderoom/internal/shell"
	"github.com/roomscript/coderoom/internal/ui/room/history/record"
)

func TestHandleInterpreterEvent_rendersShellCompletion(t *testing.T) {
	m := makeReadyModel(t)
	exitCode := 7
	event := interpreter.ShellCompleted{
		Command: `echo "hello world" | false`,
		Cwd:     ".",
		Result: shell.Result{
			Status:   shell.StatusFailure,
			ExitCode: &exitCode,
			Stdout:   "standard output",
			Stderr:   "standard error",
			Err:      errors.New("runner failure"),
		},
		Output: "status: failure\nstdout:\nstandard output\nstderr:\nstandard error\nerror:\nrunner failure",
	}

	m = presentTestRecord(m, roomstate.NewAgentRecord("shell", agent.Message{Mode: agent.ModeSingle, Content: agent.Command{Command: event.Command, Cwd: event.Cwd, Output: event.Output, ExitCode: event.Result.ExitCode}}))
	m, _ = m.handleInterpreterEvent(event)
	command := shellCommandRecord(t, m)
	if command.Command != event.Command || command.Cwd != event.Cwd || command.ExitCode != event.Result.ExitCode {
		t.Fatalf("command = %#v, want completion event values", command)
	}
	for _, text := range []string{"status: failure", "stdout:\nstandard output", "stderr:\nstandard error", "error:\n"} {
		if !strings.Contains(command.Output, text) {
			t.Errorf("command output missing %q: %q", text, command.Output)
		}
	}
}

func TestHandleInterpreterEvent_rendersCommandDefinitionSuccess(t *testing.T) {
	m := makeReadyModel(t)
	m = presentTestRecord(m, roomstate.Record{Kind: roomstate.KindSystem, Text: "[defined] /tests"})
	m, _ = m.handleInterpreterEvent(interpreter.SubmissionSucceeded{
		Raw: "/def tests /shell go test ./...",
	})
	if !hasRecord(m, record.KindSystem, "[defined] /tests") {
		t.Fatal("expected room-visible definition outcome")
	}
}

func TestHandleInterpreterEvent_shellDispatchReleasesSubmissionGate(t *testing.T) {
	m := makeReadyModel(t)
	m.submissionPending = true

	m, _ = m.handleInterpreterEvent(interpreter.SubmissionSucceeded{Raw: "/shell long-running"})
	if m.submissionPending {
		t.Fatal("shell dispatch kept submission gate closed until process completion")
	}
	if shellCommandCount(m) != 0 {
		t.Fatal("shell dispatch rendered a result before completion")
	}

	m = presentTestRecord(m, roomstate.NewAgentRecord("shell", agent.Message{Mode: agent.ModeSingle, Content: agent.Command{Command: "long-running", Cwd: ".", Output: "status: success"}}))
	m, _ = m.handleInterpreterEvent(interpreter.ShellCompleted{
		Command: "long-running",
		Cwd:     ".",
		Result:  shell.Result{Status: shell.StatusSuccess},
		Output:  "status: success",
	})
	if shellCommandCount(m) != 1 {
		t.Fatal("shell completion did not render exactly one result")
	}
}

func TestHandleInterpreterEvent_undefinedInvocationDoesNotEchoInput(t *testing.T) {
	m := makeReadyModel(t)
	m.submissionPending = true

	m, _ = m.handleInterpreterEvent(interpreter.UnknownCommand{
		Raw:  "/not-defined",
		Name: "not-defined",
	})
	if m.submissionPending {
		t.Fatal("undefined invocation kept submission gate closed")
	}
	if hasRecord(m, record.KindUserInput, "/not-defined") {
		t.Fatal("undefined invocation was appended as accepted input")
	}
	if !hasRecord(m, record.KindSystem, "error: invoke /not-defined") {
		t.Fatal("undefined invocation error was not rendered")
	}
}

func TestHandleInterpreterEvent_rendersLoopStatus(t *testing.T) {
	m := makeReadyModel(t)
	m = presentTestRecord(m, roomstate.Record{Kind: roomstate.KindSystem, Text: "[loop] turn 1/3 sent to @ada"})
	m, _ = m.handleInterpreterEvent(interpreter.LoopStatus{Message: "[loop] turn 1/3 sent to @ada"})
	if !hasRecord(m, record.KindSystem, "[loop] turn 1/3 sent to @ada") {
		t.Fatal("loop status was not rendered")
	}
}

func shellCommandCount(m Model) int {
	count := 0
	for _, rec := range m.room.HistoryRecords() {
		if rec.Kind == record.KindCommand {
			count++
		}
	}
	return count
}

func shellCommandRecord(t *testing.T, m Model) agent.Command {
	t.Helper()
	for _, rec := range m.room.HistoryRecords() {
		if rec.Kind != record.KindCommand {
			continue
		}
		if rec.Alias != shellRecordAlias {
			t.Errorf("command alias = %q, want %q", rec.Alias, shellRecordAlias)
		}
		if rec.Msg == nil {
			t.Fatal("command record has no backing message")
		}
		command, ok := rec.Msg.Content.(agent.Command)
		if !ok {
			t.Fatalf("command content = %T, want agent.Command", rec.Msg.Content)
		}
		return command
	}
	t.Fatal("expected canonical command record")
	return agent.Command{}
}

func presentTestRecord(m Model, record roomstate.Record) Model {
	m, _ = m.handleInterpreterEvent(interpreter.TranscriptChanged{Delta: roomstate.Delta{Version: 1, RecordUpdates: []roomstate.IndexedRecord{{Index: 0, Record: record}}}})
	return m
}
