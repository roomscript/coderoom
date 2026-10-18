package interpreter_test

import (
	"context"
	"testing"
	"time"

	"github.com/roomscript/coderoom/internal/interpreter"
	"github.com/roomscript/coderoom/internal/session"
	"github.com/roomscript/coderoom/internal/shell"
)

type transcriptTestObserver struct{ events chan interpreter.Event }

func (o transcriptTestObserver) OnEvent(event interpreter.Event) { o.events <- event }

func TestTranscriptStream_acceptancePrecedesRecordsAndCausalUpdatesPrecedeCompletion(t *testing.T) {
	sess := newRecordingSession()
	sess.executeHook = func(_ session.Command, observer session.Observer) {
		observer.OnEvent(session.AgentLog{Alias: testAlias("ada"), Text: "causal output"})
	}
	events := make(chan interpreter.Event, 32)
	interp := interpreter.New(context.Background(), sess, ".", interpreter.WithObserver(transcriptTestObserver{events: events}))
	receiveEvent[interpreter.StateChanged](t, events)
	t.Cleanup(interp.Close)
	if err := interp.Submit("/cancel ada"); err != nil {
		t.Fatal(err)
	}

	if event := receiveTranscriptTestEvent(t, events); !isInputAccepted(event) {
		t.Fatalf("first event = %T, want interpreter.InputAccepted", event)
	}
	texts, versions := collectSubmissionTranscript(t, events)
	if len(texts) != 2 || texts[0] != "/cancel ada" || texts[1] != "causal output" {
		t.Fatalf("transcript before completion = %v", texts)
	}
	if versions[0] >= versions[1] {
		t.Fatalf("versions = %v", versions)
	}
}

func isInputAccepted(event interpreter.Event) bool {
	_, ok := event.(interpreter.InputAccepted)
	return ok
}

func receiveTranscriptTestEvent(t *testing.T, events <-chan interpreter.Event) interpreter.Event {
	t.Helper()
	select {
	case event := <-events:
		return event
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for transcript event")
		return nil
	}
}

func collectSubmissionTranscript(t *testing.T, events <-chan interpreter.Event) ([]string, []uint64) {
	t.Helper()
	var texts []string
	var versions []uint64
	for {
		switch event := receiveTranscriptTestEvent(t, events).(type) {
		case interpreter.TranscriptChanged:
			versions = append(versions, event.Delta.Version)
			for _, update := range event.Delta.RecordUpdates {
				texts = append(texts, update.Record.Text)
			}
		case interpreter.SubmissionSucceeded:
			return texts, versions
		}
	}
}

func TestTranscriptStream_shellInputAppearsBeforeCompletion(t *testing.T) {
	for _, raw := range []string{"/shell sleep 60", "/slow"} {
		t.Run(raw, func(t *testing.T) {
			release := make(chan struct{})
			runner := interpreter.ShellRunnerFunc(func(ctx context.Context, _, _ string) shell.Result {
				select {
				case <-release:
				case <-ctx.Done():
				}
				return shell.Result{Status: shell.StatusSuccess}
			})
			events := make(chan interpreter.Event, 32)
			interp := interpreter.New(context.Background(), newRecordingSession(), ".", interpreter.WithShellRunner(runner), interpreter.WithObserver(transcriptTestObserver{events: events}))
			receiveEvent[interpreter.StateChanged](t, events)
			t.Cleanup(interp.Close)
			t.Cleanup(func() { close(release) })
			if err := interp.Submit("/def slow /shell sleep 60"); err != nil {
				t.Fatal(err)
			}
			interp.Snapshot()
			if err := interp.Submit(raw); err != nil {
				t.Fatal(err)
			}
			waitForShellInput(t, events, raw)
		})
	}
}

func waitForShellInput(t *testing.T, events <-chan interpreter.Event, raw string) {
	t.Helper()
	for {
		switch event := receiveTranscriptTestEvent(t, events).(type) {
		case interpreter.ShellCompleted:
			t.Fatal("shell completed while its runner was blocked")
		case interpreter.TranscriptChanged:
			for _, update := range event.Delta.RecordUpdates {
				if update.Record.Text == raw {
					return
				}
			}
		}
	}
}
