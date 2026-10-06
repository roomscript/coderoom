package interpreter_test

import (
	"context"
	"errors"
	"testing"

	"github.com/roomscript/coderoom/internal/interpreter"
	"github.com/roomscript/coderoom/internal/session"
)

func TestSubmitAPI_reportsUnknownCommand(t *testing.T) {
	interp, _, events := newSubmitExample()
	defer interp.Close()

	if err := interp.Submit("/not-defined"); err != nil {
		t.Fatalf("Submit: %v", err)
	}

	unknown := receiveEvent[interpreter.UnknownCommand](t, events)
	if unknown.Raw != "/not-defined" || unknown.Name != "not-defined" {
		t.Fatalf("unknown command = %#v", unknown)
	}
}

func TestSubmitAPI_rejectsInvalidArguments(t *testing.T) {
	interp, _, events := newSubmitExample()
	defer interp.Close()

	if err := interp.Submit("/invite"); err != nil {
		t.Fatalf("Submit: %v", err)
	}

	rejected := receiveEvent[interpreter.InputRejected](t, events)
	if rejected.Raw != "/invite" || rejected.Err == nil {
		t.Fatalf("rejected input = %#v", rejected)
	}
}

func TestSubmitAPI_executesNativeCommand(t *testing.T) {
	interp, sess, events := newSubmitExample()
	defer interp.Close()

	if err := interp.Submit("/cancel ada"); err != nil {
		t.Fatalf("Submit: %v", err)
	}

	accepted := receiveEvent[interpreter.InputAccepted](t, events)
	if accepted.Raw != "/cancel ada" {
		t.Fatalf("accepted input = %#v", accepted)
	}
	command := receiveCommand(t, sess.executed)
	want := session.CancelCommand{Alias: "ada"}
	if command != want {
		t.Fatalf("native command = %#v, want %#v", command, want)
	}
}

func TestSubmitAPI_rejectsOwnershipAfterClose(t *testing.T) {
	interp, _, _ := newSubmitExample()
	interp.Close()

	if err := interp.Submit("/who"); !errors.Is(err, interpreter.ErrClosed) {
		t.Fatalf("Submit error = %v, want ErrClosed", err)
	}
}

func newSubmitExample() (*interpreter.Interpreter, *recordingSession, chan interpreter.Event) {
	sess := newRecordingSession()
	events := make(chan interpreter.Event, 8)
	interp := interpreter.New(context.Background(), sess, ".", interpreter.WithObserver(eventObserver{events: events}))
	<-events // Consume initial StateChanged.
	return interp, sess, events
}
