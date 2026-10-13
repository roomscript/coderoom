package interpreter_test

import (
	"context"
	"errors"
	"testing"

	"github.com/roomscript/coderoom/internal/interpreter"
	"github.com/roomscript/coderoom/internal/participant"
)

type admissionObserver struct{ events chan interpreter.Event }

func (o admissionObserver) OnEvent(event interpreter.Event) {
	if _, rejected := event.(interpreter.InputRejected); rejected {
		o.events <- event
	}
}

func TestInterpreter_WhoPublishesOneCanonicalNotice(t *testing.T) {
	for _, tt := range []struct {
		name   string
		views  []participant.View
		notice string
	}{
		{name: "empty", notice: "[no agents]"},
		{name: "starting", views: []participant.View{{Alias: testAlias("ada"), Status: participant.StatusStarting}}, notice: "[agents] ada"},
		{name: "sorted", views: []participant.View{{Alias: testAlias("tim")}, {Alias: testAlias("ada")}}, notice: "[agents] ada, tim"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			sess := newRecordingSession()
			sess.participants = tt.views
			interp := interpreter.New(context.Background(), sess, t.TempDir())
			t.Cleanup(interp.Close)
			if err := interp.Submit("/who"); err != nil {
				t.Fatal(err)
			}
			interp.Close()
			records := interp.Snapshot().Room.Records
			if len(records) != 2 || records[0].Text != "/who" || records[1].Text != tt.notice {
				t.Fatalf("canonical records = %#v", records)
			}
			if len(sess.executed) != 0 {
				t.Fatal("participant listing executed a Session command")
			}
		})
	}
}

func TestInterpreter_WhoPreservesPendingStageAdmission(t *testing.T) {
	for _, raw := range []string{"/who", "/who unexpected"} {
		t.Run(raw, func(t *testing.T) {
			sess := newRecordingSession()
			sess.participants = []participant.View{{Alias: testAlias("ada"), Status: participant.StatusWorking, StartupReady: true}}
			events := make(chan interpreter.Event, 64)
			interp := interpreter.New(context.Background(), sess, t.TempDir(), interpreter.WithObserver(admissionObserver{events: events}))
			t.Cleanup(interp.Close)
			if err := interp.Submit("@ada retained work"); err != nil {
				t.Fatal(err)
			}
			before := interp.Snapshot()
			if before.Stage == nil {
				t.Fatal("busy send was not staged")
			}
			if err := interp.Submit(raw); err != nil {
				t.Fatal(err)
			}
			rejected := receiveEvent[interpreter.InputRejected](t, events)
			if rejected.Raw != raw || !errors.Is(rejected.Err, interpreter.ErrStagePending) {
				t.Fatalf("stage admission rejection = %#v", rejected)
			}
			after := interp.Snapshot()
			if after.Stage == nil || after.Stage.Raw != before.Stage.Raw || len(after.Room.Records) != len(before.Room.Records) {
				t.Fatal("blocked modular submission changed stage or transcript")
			}
		})
	}
}
