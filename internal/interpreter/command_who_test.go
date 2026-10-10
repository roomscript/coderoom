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
			assertWhoTranscript(t, events, tt.notice)
			state := receiveSubmitEvent[StateChanged](t, events)
			if len(state.Snapshot.Room.Records) != 2 {
				t.Fatalf("snapshot records = %#v, want two", state.Snapshot.Room.Records)
			}
			succeeded := receiveSubmitEvent[SubmissionSucceeded](t, events)
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

// assertWhoTranscript checks the transport order without the semantic helper,
// which intentionally skips transcript deltas.
func assertWhoTranscript(t *testing.T, events <-chan Event, notice string) {
	t.Helper()
	var records []room.Record
	var version uint64
	for len(records) < 2 {
		event := receiveTranscriptTestEvent(t, events)
		change, ok := event.(TranscriptChanged)
		if !ok {
			t.Fatalf("event = %T, want TranscriptChanged", event)
		}
		if change.Delta.Version <= version {
			t.Fatal("transcript versions did not advance")
		}
		version = change.Delta.Version
		for _, update := range change.Delta.RecordUpdates {
			if update.Index != len(records) {
				t.Fatalf("record index = %d, want %d", update.Index, len(records))
			}
			records = append(records, update.Record)
		}
	}
	want := []room.Record{{Kind: room.KindUserInput, Text: "/who"}, {Kind: room.KindSystem, Text: notice}}
	if !reflect.DeepEqual(records, want) {
		t.Fatalf("records = %#v, want %#v", records, want)
	}
}
