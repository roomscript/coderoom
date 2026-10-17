package interpreter_test

import (
	"context"
	"errors"
	"testing"

	"github.com/roomscript/coderoom/internal/interpreter"
	"github.com/roomscript/coderoom/internal/promptlang"
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
	for _, raw := range []string{"  /invite  ", "", "/who extra", "/loop @ada work"} {
		t.Run(raw, func(t *testing.T) {
			interp, sess, events := newSubmitExample()
			defer interp.Close()
			_, expected := promptlang.Parse(raw)
			if err := interp.Submit(raw); err != nil {
				t.Fatal(err)
			}
			rejected := receiveEvent[interpreter.InputRejected](t, events)
			assertParseRejection(t, rejected, raw, expected)
			interp.Close()
			if records := interp.Snapshot().Room.Records; len(records) != 0 {
				t.Fatalf("invalid input recorded=%#v", records)
			}
			assertSessionCommandSettled(t, sess, events)
		})
	}
}

func assertParseRejection(t *testing.T, rejected interpreter.InputRejected, raw string, expected error) {
	t.Helper()
	var got, want *promptlang.Diagnostic
	if !errors.As(rejected.Err, &got) || !errors.As(expected, &want) {
		t.Fatalf("diagnostics=%v, %v", rejected.Err, expected)
	}
	if rejected.Raw != raw || rejected.Code != interpreter.ErrorInvalidInput || got.Code != want.Code || got.Span != want.Span {
		t.Fatalf("rejection=%#v diagnostic=%#v want=%#v", rejected, got, want)
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
	want := session.CancelCommand{Alias: testAlias("ada")}
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
