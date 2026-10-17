package interpreter_test

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/roomscript/coderoom/internal/interpreter"
	"github.com/roomscript/coderoom/internal/session"
)

func TestUnroutedCommands_shutdownDrainsAdmittedSubmission(t *testing.T) {
	for _, raw := range []string{"/policy enable send-notices", "/invite ada", "/remove ada", "/cancel ada"} {
		for _, failed := range []bool{false, true} {
			t.Run(raw+map[bool]string{false: "/success", true: "/failure"}[failed], func(t *testing.T) {
				assertUnroutedShutdown(t, raw, failed)
			})
		}
	}
}

func assertUnroutedShutdown(t *testing.T, raw string, failed bool) {
	t.Helper()
	interp, sess, events := newSubmitExample()
	entered, releaseExecution := make(chan struct{}), make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(releaseExecution) }) }
	t.Cleanup(interp.Close)
	t.Cleanup(release)
	if failed {
		sess.executeErr = errors.New("execution rejected")
	}
	sess.executeHook = func(session.Command, session.Observer) { close(entered); <-releaseExecution }
	if err := interp.Submit(raw); err != nil {
		t.Fatal(err)
	}
	receiveEvent[interpreter.InputAccepted](t, events)
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("Session execution did not start")
	}
	closed := make(chan struct{})
	go func() { interp.Close(); close(closed) }()
	awaitClosedPublicAdmission(t, interp)
	select {
	case <-closed:
		t.Fatal("Close returned while admitted execution was blocked")
	default:
	}
	release()
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not drain admitted execution")
	}
	assertUnroutedShutdownOutcome(t, events, raw, sess.executeErr)
	receiveCommand(t, sess.executed)
	assertNotExecuted(t, sess.executed)
	if sess.shutdowns.Load() != 1 {
		t.Fatalf("shutdown calls=%d", sess.shutdowns.Load())
	}
}

// A harmless undefined command lets the test observe admission closing without
// consulting monolith state or assuming that the Close goroutine has run.
func awaitClosedPublicAdmission(t *testing.T, interp *interpreter.Interpreter) {
	t.Helper()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	timeout := time.After(2 * time.Second)
	for {
		select {
		case <-ticker.C:
			if err := interp.Submit("/shutdown-probe"); errors.Is(err, interpreter.ErrClosed) {
				return
			}
		case <-timeout:
			t.Fatal("Close did not close public admission")
		}
	}
}

func assertUnroutedShutdownOutcome(t *testing.T, events <-chan interpreter.Event, raw string, want error) {
	t.Helper()
	var outcomes []error
	for len(events) > 0 {
		switch event := (<-events).(type) {
		case interpreter.SubmissionSucceeded:
			if event.Raw == raw {
				outcomes = append(outcomes, nil)
			}
		case interpreter.SubmissionFailed:
			if event.Raw == raw {
				outcomes = append(outcomes, event.Err)
			}
		case interpreter.OperationFailed:
			t.Fatalf("unrouted submission reported a separate operation failure: %v", event.Err)
		}
	}
	if len(outcomes) != 1 || !errors.Is(outcomes[0], want) {
		t.Fatalf("submission outcomes=%v, want one outcome with %v", outcomes, want)
	}
}
