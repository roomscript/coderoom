package interpreter

import (
	"context"
	"reflect"
	"testing"

	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/promptlang"
	"github.com/roomscript/coderoom/internal/room"
)

func TestSubmitContract_whoPublishesCanonicalNoticeOnce(t *testing.T) {
	tests := []struct {
		name         string
		participants []participant.View
		notice       string
	}{
		{name: "empty", notice: "[no agents]"},
		{name: "starting", participants: []participant.View{{Alias: "ada", Status: participant.StatusStarting}}, notice: "[agents] ada"},
		{name: "sorted", participants: []participant.View{{Alias: "tim"}, {Alias: "ada"}}, notice: "[agents] ada, tim"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sess := newSubmitContractSession()
			sess.participants = tt.participants
			events := make(chan Event, 32)
			interp := New(context.Background(), sess, t.TempDir(), WithObserver(transcriptTestObserver{events: events}))
			t.Cleanup(interp.Close)
			receiveSubmitEvent[StateChanged](t, events)

			mustSubmit(t, interp.Submit("/who"))
			accepted := receiveSubmitEvent[InputAccepted](t, events)
			if _, ok := accepted.Statement.Value.(promptlang.Who); !ok {
				t.Fatalf("accepted statement = %T, want Who", accepted.Statement.Value)
			}
			succeeded := assertCommandLaunch(t, events, "/who")
			assertWhoTranscript(t, events, tt.notice)
			state := receiveSubmitEvent[StateChanged](t, events)
			if len(state.Snapshot.Room.Records) != 2 {
				t.Fatalf("snapshot records = %#v, want two", state.Snapshot.Room.Records)
			}
			if succeeded.Raw != "/who" {
				t.Fatalf("completion raw = %q", succeeded.Raw)
			}
			if _, ok := succeeded.Statement.Value.(promptlang.Who); !ok {
				t.Fatalf("completion statement = %T, want Who", succeeded.Statement.Value)
			}
			assertNoSubmitExecution(t, sess.executed)
			assertNoSubmitEvent(t, events)
		})
	}
}

func TestSubmitContract_whoCompletesBeforeShutdown(t *testing.T) {
	for _, blocked := range []bool{false, true} {
		name := "immediate close"
		if blocked {
			name = "accepted submission executes during shutdown"
		}
		t.Run(name, func(t *testing.T) {
			sess := newSubmitContractSession()
			sess.participants = []participant.View{{Alias: "ada"}}
			events := make(chan Event, 32)
			interp := New(context.Background(), sess, t.TempDir(), WithObserver(transcriptTestObserver{events: events}))
			t.Cleanup(interp.Close)
			receiveSubmitEvent[StateChanged](t, events)
			blocker := blockingSubmitContractOperation{entered: make(chan struct{}), release: make(chan struct{})}
			if blocked {
				if !interp.executor.enqueue(blocker) {
					t.Fatal("could not enqueue blocker")
				}
				<-blocker.entered
			}
			mustSubmit(t, interp.Submit("/who"))
			if blocked {
				// Begin Close before releasing the accepted submission, deterministically.
				interp.executor.requestClose()
				close(blocker.release)
			}
			interp.Close()
			receiveSubmitEvent[InputAccepted](t, events)
			assertCommandLaunch(t, events, "/who")
			assertWhoTranscript(t, events, "[agents] ada")
			receiveSubmitEvent[StateChanged](t, events)
			if got := interp.Snapshot().Room.Records; len(got) != 2 || got[1].Text != "[agents] ada" {
				t.Fatalf("final records = %#v, want input and agent listing", got)
			}
			assertNoSubmitEvent(t, events)
		})
	}
}

// assertCommandLaunch checks the input delta and acknowledgement before output.
func assertCommandLaunch(t *testing.T, events <-chan Event, input string) SubmissionSucceeded {
	t.Helper()
	event := receiveTranscriptTestEvent(t, events)
	change, ok := event.(TranscriptChanged)
	if !ok {
		t.Fatalf("event = %T, want input transcript delta", event)
	}
	updates := change.Delta.RecordUpdates
	if len(updates) != 1 || updates[0].Index != 0 || !reflect.DeepEqual(updates[0].Record, room.Record{Kind: room.KindUserInput, Text: input}) {
		t.Fatalf("input updates = %#v", updates)
	}
	return receiveSubmitEvent[SubmissionSucceeded](t, events)
}

func assertWhoTranscript(t *testing.T, events <-chan Event, notice string) {
	t.Helper()
	assertCommandTranscript(t, events, notice)
}

func assertCommandTranscript(t *testing.T, events <-chan Event, notice string) {
	t.Helper()
	event := receiveTranscriptTestEvent(t, events)
	change, ok := event.(TranscriptChanged)
	if !ok {
		t.Fatalf("event = %T, want output transcript delta", event)
	}
	updates := change.Delta.RecordUpdates
	if len(updates) != 1 || updates[0].Index != 1 || change.Delta.Version < 2 || !reflect.DeepEqual(updates[0].Record, room.Record{Kind: room.KindSystem, Text: notice}) {
		t.Fatalf("output delta = %#v", change.Delta)
	}
}
