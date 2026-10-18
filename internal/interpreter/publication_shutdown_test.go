package interpreter_test

import (
	"sync"
	"testing"
	"time"

	"github.com/roomscript/coderoom/internal/interpreter"
)

type gatedAcceptedObserver struct {
	events  chan interpreter.Event
	entered chan struct{}
	release chan struct{}
}

func (o gatedAcceptedObserver) OnEvent(event interpreter.Event) {
	if _, transcript := event.(interpreter.TranscriptChanged); transcript {
		return
	}
	if _, accepted := event.(interpreter.InputAccepted); accepted {
		close(o.entered)
		<-o.release
	}
	o.events <- event
}

func TestClose_flushesAcceptedSubmissionThroughSlowObserver(t *testing.T) {
	events := make(chan interpreter.Event, 64)
	entered, released := make(chan struct{}), make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(released) }) }
	sess := newRecordingSession()
	interp := interpreter.New(t.Context(), sess, t.TempDir(), interpreter.WithObserver(gatedAcceptedObserver{events: events, entered: entered, release: released}))
	t.Cleanup(interp.Close)
	t.Cleanup(release)
	receiveEvent[interpreter.StateChanged](t, events)
	if err := interp.Submit("/cancel ada"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("acceptance did not reach observer")
	}
	receiveCommand(t, sess.executed)
	// Slow observation must not hold the interpreter's execution lane.
	interp.Snapshot()
	closed := make(chan struct{})
	go func() { interp.Close(); close(closed) }()
	select {
	case <-closed:
		t.Fatal("Close returned before queued outcomes reached the observer")
	case <-time.After(20 * time.Millisecond):
	}
	release()
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not flush observer publications")
	}
	receiveEvent[interpreter.InputAccepted](t, events)
	receiveEvent[interpreter.StateChanged](t, events)
	assertSessionSubmissionOutcome(t, events, "/cancel ada", "cancel", nil)
	assertSessionCommandSettled(t, sess, events)
}
