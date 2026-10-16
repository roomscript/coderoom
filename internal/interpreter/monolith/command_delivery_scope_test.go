package monolith

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/roomscript/coderoom/internal/interpreter/runtime"
	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/session"
)

func TestDeliveryCapability_ScopesIdenticalBroadcastRecipients(t *testing.T) {
	interp, sess, _ := newSubmitContractInterpreter(t)
	firstErr, secondErr := errors.New("first"), errors.New("second")
	sess.routingOverride = true
	sess.execute = func(command session.Command, observer session.Observer) {
		broadcast := command.(session.BroadcastCommand)
		err := firstErr
		if broadcast.Text == "second" {
			err = secondErr
		}
		observer.OnEvent(session.RoutingCompleted{Result: session.RoutingResult{Kind: session.RoutingBroadcast, Err: err,
			Recipients: []session.RecipientResult{{Alias: testAlias("ada"), Role: session.RecipientBroadcast, Status: session.DeliveryFailed}}}})
	}
	results := []chan runtime.ExecutionEvent{make(chan runtime.ExecutionEvent, 4), make(chan runtime.ExecutionEvent, 4)}
	submissions := make([]*CommandSubmission, 2)
	mustSubmit(t, interp.Schedule(func() {
		for index, text := range []string{"first", "second"} {
			submission := interp.AdmitCommand("broadcast", parseSourceStatement(t, text))
			submissions[index] = submission
			execution := submission.Context.Session.CreateExecution()
			execution.Subscribe(func(event runtime.ExecutionEvent) { results[index] <- event })
			command := session.BroadcastCommand{Aliases: []participant.Alias{testAlias("ada")}, Text: text}
			if err := execution.Execute(command); err != nil {
				t.Error(err)
			}
		}
		// Both listeners are active. A legacy dispatch with identical recipients must
		// reach neither stream, and each native dispatch must reach only its owner.
		if err := interp.executor.executeCommand(session.BroadcastCommand{Aliases: []participant.Alias{testAlias("ada")}, Text: "legacy"}); err != nil {
			t.Error(err)
		}
		interp.executor.applySessionEvents()
	}))
	interp.Snapshot()
	assertScopedRoutingEvents(t, results, []error{firstErr, secondErr})
	mustSubmit(t, interp.Schedule(func() {
		for _, submission := range submissions {
			submission.Close()
		}
	}))
}

func TestDeliveryCapability_ProtocolFailureWithoutRoutingMetadata(t *testing.T) {
	for _, scenario := range []string{"missing", "empty", "multiple"} {
		t.Run(scenario, func(t *testing.T) {
			interp, sess, _ := newSubmitContractInterpreter(t)
			sess.routingOverride = true
			sess.execute = func(_ session.Command, observer session.Observer) {
				switch scenario {
				case "empty":
					observer.OnEvent(session.RoutingCompleted{})
				case "multiple":
					observer.OnEvent(session.RoutingCompleted{Result: session.RoutingResult{Kind: "probe"}})
					observer.OnEvent(session.RoutingCompleted{Result: session.RoutingResult{Kind: "probe"}})
				}
			}
			events := make(chan runtime.ExecutionEvent, 2)
			mustSubmit(t, interp.Schedule(func() {
				submission := interp.AdmitCommand("probe", parseSourceStatement(t, "/cancel ada"))
				execution := submission.Context.Session.CreateExecution()
				execution.Subscribe(func(event runtime.ExecutionEvent) { events <- event; submission.Close() })
				// No recipient template or routing kind is needed for a protocol failure.
				if err := execution.Execute(session.CancelCommand{Alias: testAlias("ada")}); err != nil {
					t.Error(err)
				}
			}))
			interp.Snapshot()
			if len(events) != 1 {
				t.Fatal("protocol failure did not emit one terminal event")
			}
			failure, ok := (<-events).(runtime.ExecutionFailed)
			want := runtime.ErrInvalidDeliveryOutcome
			if scenario == "missing" {
				want = errMissingRoutingOutcome
			}
			if !ok || !errors.Is(failure.Err, want) {
				t.Fatalf("terminal failure=%#v", failure)
			}
		})
	}
}

func assertScopedRoutingEvents(t *testing.T, events []chan runtime.ExecutionEvent, expected []error) {
	t.Helper()
	for index, want := range expected {
		if len(events[index]) != 1 {
			t.Fatalf("scope %d received %d events", index, len(events[index]))
		}
		result, ok := (<-events[index]).(runtime.DeliveryCompleted)
		if !ok || !errors.Is(result.Result.Err, want) {
			t.Fatalf("scope %d consumed another execution: %#v", index, result)
		}
	}
}

func TestDeliveryCapability_NoSharedPrimaryReservation(t *testing.T) {
	interp, sess, _ := newSubmitContractInterpreter(t)
	completed := make(chan runtime.ExecutionEvent, 2)
	mustSubmit(t, interp.Schedule(func() {
		for range 2 {
			submission := interp.AdmitCommand("send", parseSourceStatement(t, "@ada work"))
			execution := submission.Context.Session.CreateExecution()
			execution.Subscribe(func(event runtime.ExecutionEvent) { completed <- event; submission.Close() })
			command := session.SendToParticipantCommand{Plan: submission.Context.Session.CreateParticipantSendPlan(testAlias("ada")), Message: "work"}
			if err := execution.Execute(command); err != nil {
				t.Error(err)
			}
		}
	}))
	interp.Snapshot()
	// Session decides availability. SubmitCommand's reservation remains private.
	if len(completed) != 2 || len(sess.executed) != 2 {
		t.Fatal("native capability rejected a command before Session execution")
	}
	if sess.maxActive.Load() != 1 {
		t.Fatal("native commands executed concurrently")
	}
}

// Use different returned IDs even for deliveries to identical recipients. The
// adapter must forward the actual execution identity, including failed delivery.
type identifiedDeliverySession struct {
	*submitContractSession
	next atomic.Uint64
}

func (s *identifiedDeliverySession) Execute(command session.Command) (session.ExecutionID, error) {
	id := session.ExecutionID(s.next.Add(1))
	_, err := s.submitContractSession.Execute(command)
	return id, err
}

func TestDeliveryCapability_ForwardsExecutionID(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(map[bool]string{false: "delivered", true: "failed"}[failed], func(t *testing.T) {
			sess := &identifiedDeliverySession{submitContractSession: newSubmitContractSession()}
			sess.next.Store(40)
			if failed {
				sess.executeErr = errors.New("delivery rejected")
			}
			interp := New(context.Background(), sess, t.TempDir())
			t.Cleanup(interp.Close)
			results := make(chan runtime.ExecutionEvent, 2)
			mustSubmit(t, interp.Schedule(func() {
				for _, text := range []string{"first", "second"} {
					submission := interp.AdmitCommand("broadcast", parseSourceStatement(t, text))
					execution := submission.Context.Session.CreateExecution()
					execution.Subscribe(func(event runtime.ExecutionEvent) { results <- event; submission.Close() })
					if err := execution.Execute(session.BroadcastCommand{Aliases: []participant.Alias{testAlias("ada")}, Text: text}); err != nil {
						t.Error(err)
					}
				}
			}))
			interp.Snapshot() // Drain scheduled admission and native dispatch before shutdown.
			interp.Close()
			if len(results) != 2 {
				t.Fatalf("terminal events=%d", len(results))
			}
			for _, id := range []session.ExecutionID{41, 42} {
				result, ok := (<-results).(runtime.DeliveryCompleted)
				if !ok || result.ExecutionID != id || !errors.Is(result.Result.Err, sess.executeErr) {
					t.Fatalf("result=%#v, want execution=%d", result, id)
				}
			}
		})
	}
}
