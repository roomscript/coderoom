package interpreter

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/roomscript/coderoom/internal/promptlang"
	"github.com/roomscript/coderoom/internal/session"
)

func newApplicationInterpreter(t *testing.T, commands ...ApplicationCommand) (*Interpreter, chan Event) {
	t.Helper()
	events := make(chan Event, 128)
	interp, err := NewWithApplicationCommands(t.Context(), session.New(), t.TempDir(), commands, WithObserver(routingObserver{events}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(interp.Close)
	return interp, events
}

func TestApplicationCommands_dispatchAndFailure(t *testing.T) {
	failure := errors.New("dispatch failed")
	for _, outcome := range []struct {
		name string
		err  error
	}{{"accepted", nil}, {"failed", failure}} {
		t.Run(outcome.name, func(t *testing.T) {
			var calls atomic.Int32
			interp, events := newApplicationInterpreter(t, ApplicationCommand{Name: "inspect", Run: func() error { calls.Add(1); return outcome.err }})
			const raw = "  /inspect  "
			if err := interp.Submit(raw); err != nil {
				t.Fatal(err)
			}
			awaitLifecycleEvent(t, events, func(event Event) bool {
				if outcome.err == nil {
					succeeded, ok := event.(SubmissionSucceeded)
					return ok && succeeded.Raw == raw
				}
				failed, ok := event.(SubmissionFailed)
				return ok && failed.Raw == raw && errors.Is(failed.Err, outcome.err)
			})
			interp.Close()
			if calls.Load() != 1 {
				t.Fatal("callback was not called once")
			}
			records := interp.Snapshot().Room.Records
			if len(records) != 1 || records[0].Text != raw {
				t.Fatalf("records=%+v", records)
			}
			assertNoSubmissionOutcome(t, events)
		})
	}
}

func TestApplicationCommands_rejectInvalidRegistrations(t *testing.T) {
	noop := func() error { return nil }
	for _, commands := range [][]ApplicationCommand{
		{{Name: "inspect"}},
		{{Name: "help", Run: noop}},
		{{Name: "bad name", Run: noop}},
		{{Name: "inspect", Run: noop}, {Name: "inspect", Run: noop}},
	} {
		events := make(chan Event, 4)
		sess := session.New()
		t.Cleanup(sess.Shutdown)
		interp, err := NewWithApplicationCommands(t.Context(), sess, t.TempDir(), commands, WithObserver(routingObserver{events}))
		if interp != nil {
			interp.Close()
			t.Fatal("invalid registration started interpreter")
		}
		if err == nil || len(events) != 0 {
			t.Fatal("invalid batch published output or returned no error")
		}
	}
}

func TestApplicationCommands_cannotBeRedefinedOrUsedAsCondition(t *testing.T) {
	var calls atomic.Int32
	interp, events := newApplicationInterpreter(t, ApplicationCommand{Name: "inspect", Run: func() error { calls.Add(1); return nil }})
	if err := interp.Submit("/def inspect /shell true"); err != nil {
		t.Fatal(err)
	}
	awaitLifecycleEvent(t, events, func(event Event) bool {
		failed, ok := event.(SubmissionFailed)
		return ok && failed.Code == ErrorCommandExists
	})
	if err := interp.Submit("/loop @missing work /until /inspect /max 1"); err != nil {
		t.Fatal(err)
	}
	awaitLifecycleEvent(t, events, func(event Event) bool {
		var notShell promptlang.NotShellCommandError
		failed, ok := event.(SubmissionFailed)
		return ok && errors.As(failed.Err, &notShell)
	})
	if calls.Load() != 0 {
		t.Fatal("registration ran as a shell or loop condition")
	}
}

func TestApplicationCommands_registrationIsCopied(t *testing.T) {
	var calls atomic.Int32
	commands := []ApplicationCommand{{Name: "inspect", Run: func() error { calls.Add(1); return nil }}}
	interp, events := newApplicationInterpreter(t, commands...)
	commands[0] = ApplicationCommand{Name: "changed", Run: func() error { t.Error("modified callback ran"); return nil }}
	if err := interp.Submit("/inspect"); err != nil {
		t.Fatal(err)
	}
	awaitLifecycleEvent(t, events, func(event Event) bool { _, ok := event.(SubmissionSucceeded); return ok })
	if calls.Load() != 1 {
		t.Fatal("registration changed with caller slice")
	}
}

func TestApplicationCommands_participantSubmissionStillUsesStageAdmission(t *testing.T) {
	var calls atomic.Int32
	var interp *Interpreter
	command := ApplicationCommand{Name: "inspect", Run: func() error {
		calls.Add(1)
		return interp.Submit("@ada nested")
	}}
	created, sess, _, events, alias := newLifecycleInterpreter(t, command)
	interp = created
	if _, err := sess.Execute(session.SendToParticipantCommand{Plan: sess.CreateParticipantSendPlan(alias), Message: "busy"}); err != nil {
		t.Fatal(err)
	}
	if err := interp.Submit("@ada work"); err != nil {
		t.Fatal(err)
	}
	awaitLifecycleEvent(t, events, func(event Event) bool {
		changed, ok := event.(StateChanged)
		return ok && changed.Snapshot.Stage != nil
	})
	if err := interp.Submit("/inspect"); err != nil {
		t.Fatal(err)
	}
	awaitApplicationAndParticipantAdmission(t, events)
	if calls.Load() != 1 || interp.Snapshot().Stage == nil {
		t.Fatal("application did not run once or displaced the retained stage")
	}
}

func awaitApplicationAndParticipantAdmission(t *testing.T, events <-chan Event) {
	t.Helper()
	accepted, rejected := false, false
	awaitLifecycleEvent(t, events, func(event Event) bool {
		switch event := event.(type) {
		case SubmissionSucceeded:
			accepted = accepted || event.Raw == "/inspect"
		case InputRejected:
			rejected = rejected || (event.Raw == "@ada nested" && event.Code == ErrorStagePending)
		}
		return accepted && rejected
	})
}

func TestApplicationCommands_rejectArguments(t *testing.T) {
	var calls atomic.Int32
	interp, events := newApplicationInterpreter(t, ApplicationCommand{Name: "inspect", Run: func() error { calls.Add(1); return nil }})
	if err := interp.Submit("/inspect extra"); err != nil {
		t.Fatal(err)
	}
	awaitLifecycleEvent(t, events, func(event Event) bool {
		rejected, ok := event.(InputRejected)
		return ok && rejected.Code == ErrorInvalidInput
	})
	if calls.Load() != 0 {
		t.Fatal("invalid syntax ran callback")
	}
}

func TestApplicationCommands_shutdownDrainsOrderedCallback(t *testing.T) {
	entered := make(chan struct{})
	released := make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(released) }) }
	interp, events := newApplicationInterpreter(t, ApplicationCommand{Name: "inspect", Run: func() error { close(entered); <-released; return nil }})
	t.Cleanup(release)
	if err := interp.Submit("/inspect"); err != nil {
		t.Fatal(err)
	}
	awaitShellSignal(t, entered)
	progressed := make(chan struct{})
	go func() { interp.Snapshot(); close(progressed) }()
	awaitShellSignal(t, progressed)
	closed := make(chan struct{})
	go func() { interp.Close(); close(closed) }()
	select {
	case <-closed:
		t.Fatal("shutdown did not wait for admitted callback")
	default:
	}
	release()
	awaitShellSignal(t, closed)
	awaitLifecycleEvent(t, events, func(event Event) bool {
		succeeded, ok := event.(SubmissionSucceeded)
		return ok && succeeded.Raw == "/inspect"
	})
	assertNoSubmissionOutcome(t, events)
}
