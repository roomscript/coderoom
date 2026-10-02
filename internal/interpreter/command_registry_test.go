package interpreter_test

import (
	"context"
	"testing"

	"github.com/trigosec/coderoom/internal/interpreter"
	"github.com/trigosec/coderoom/internal/promptlang"
)

func TestInterpreter_ownsRoomScopedCommandDefinitions(t *testing.T) {
	firstEvents := make(chan interpreter.Event, 8)
	first := interpreter.New(context.Background(), newRecordingSession(), t.TempDir(), interpreter.WithObserver(eventObserver{events: firstEvents}))
	receiveEvent[interpreter.StateChanged](t, firstEvents)
	t.Cleanup(first.Close)
	second := interpreter.New(context.Background(), newRecordingSession(), t.TempDir())
	t.Cleanup(second.Close)
	definition := promptlang.CommandDefinition{
		Name: "tests",
		Body: promptlang.Shell{Program: "go test ./..."},
	}

	if err := first.Submit("/def tests /shell go test ./..."); err != nil {
		t.Fatalf("Submit definition: %v", err)
	}
	receiveEvent[interpreter.InputAccepted](t, firstEvents)
	receiveEvent[interpreter.StateChanged](t, firstEvents)
	receiveEvent[interpreter.SubmissionSucceeded](t, firstEvents)
	body, err := first.ResolveCommand(promptlang.CommandInvocation{Name: "tests"})
	if err != nil {
		t.Fatalf("ResolveCommand: %v", err)
	}
	if body != definition.Body {
		t.Fatalf("body = %#v, want %#v", body, definition.Body)
	}
	if _, err := second.ResolveCommand(promptlang.CommandInvocation{Name: "tests"}); err == nil {
		t.Fatal("second interpreter resolved command defined in first interpreter")
	}
}
