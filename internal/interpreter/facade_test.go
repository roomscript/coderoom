package interpreter_test

import (
	"context"
	"testing"
	"time"

	"github.com/roomscript/coderoom/internal/interpreter"
	"github.com/roomscript/coderoom/internal/session"
)

type facadeSession struct {
	*recordingSession
	observers int
}

func (s *facadeSession) AddObserver(observer session.Observer) {
	s.observers++
	s.recordingSession.AddObserver(observer)
}

func TestInterpreter_FacadeKeepsOneRuntimeAndAppliesOptionsBeforeStartup(t *testing.T) {
	sess := &facadeSession{recordingSession: newRecordingSession()}
	events := make(chan interpreter.Event, 32)
	var configured *interpreter.Interpreter
	interp := interpreter.New(context.Background(), sess, t.TempDir(), func(i *interpreter.Interpreter) {
		configured = i
		interpreter.WithObserver(eventObserver{events: events})(i)
	})
	t.Cleanup(interp.Close)
	if configured != interp || sess.observers != 1 {
		t.Fatal("facade configured another instance or installed multiple Session observers")
	}
	select {
	case event := <-events:
		if _, ok := event.(interpreter.StateChanged); !ok {
			t.Fatalf("first publication = %T, want initial state", event)
		}
	case <-time.After(time.Second):
		t.Fatal("initial state was not published")
	}
	interp.Close()
	interp.Close()
	if sess.shutdowns.Load() != 1 {
		t.Fatal("facade did not retain one shutdown owner")
	}
}
