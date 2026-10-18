package monolith

import (
	"context"
	"errors"
	"sync"
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
		observer.OnEvent(session.RoutingCompleted{Result: session.RoutingResult{Kind: session.RoutingBroadcast,
			Recipients: []session.RecipientResult{{Alias: testAlias("ada"), Role: session.RecipientBroadcast, Status: session.DeliveryFailed, Err: err}}}})
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
	for _, scenario := range []string{"empty", "multiple"} {
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
		result, ok := (<-events[index]).(runtime.ExecutionCompleted)
		if !ok || !errors.Is(result.Result.Recipients[0].Err, want) {
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
	// Session decides availability; the capability reserves no primary.
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
				result, ok := (<-results).(runtime.ExecutionCompleted)
				if !ok || result.ExecutionID != id || !errors.Is(result.Result.Err, sess.executeErr) {
					t.Fatalf("result=%#v, want execution=%d", result, id)
				}
			}
		})
	}
}

func TestExecutionCapability_CompletesWithoutRouting(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "failure"}[failed], func(t *testing.T) {
			sess := &identifiedDeliverySession{submitContractSession: newSubmitContractSession()}
			sess.next.Store(40)
			sess.routingOverride = true
			entered, release := make(chan struct{}), make(chan struct{})
			sess.execute = func(session.Command, session.Observer) { close(entered); <-release }
			if failed {
				sess.executeErr = errors.New("cancel rejected")
			}
			interp := New(context.Background(), sess, t.TempDir())
			t.Cleanup(interp.Close)
			results := make(chan runtime.ExecutionEvent, 2)
			mustSubmit(t, interp.Schedule(func() {
				submission := interp.AdmitCommand("probe", parseSourceStatement(t, "/cancel ada"))
				execution := submission.Context.Session.CreateExecution()
				execution.Subscribe(func(event runtime.ExecutionEvent) { results <- event; submission.Close() })
				if err := execution.Execute(session.CancelCommand{Alias: testAlias("ada")}); err != nil {
					t.Error(err)
				}
			}))
			// Close after admission while Session execution is still running.
			<-entered
			interp.executor.requestClose()
			close(release)
			interp.Close()
			if len(results) != 1 {
				t.Fatalf("terminal events=%d", len(results))
			}
			result, ok := (<-results).(runtime.ExecutionCompleted)
			assertNoExecutionRouting(t, result.Result)
			if !ok || result.ExecutionID != 41 || !errors.Is(result.Err, sess.executeErr) {
				t.Fatalf("completion=%#v", result)
			}
		})
	}
}

func TestNativeDeliveryEvent_UsesExecutionError(t *testing.T) {
	failure := errors.New("Session execution failed")
	stale := errors.New("stale routing error")
	for _, err := range []error{nil, failure} {
		event := nativeDeliveryEvent(41, []session.Event{session.RoutingCompleted{Result: session.RoutingResult{Kind: session.RoutingBroadcast, Err: stale}}}, err)
		result, ok := event.(runtime.ExecutionCompleted)
		if !ok || result.ExecutionID != 41 || !errors.Is(result.Err, err) || !errors.Is(result.Result.Err, err) {
			t.Fatalf("completion=%#v", event)
		}
	}
}

func assertNoExecutionRouting(t *testing.T, routing session.RoutingResult) {
	t.Helper()
	if routing.Kind != "" || routing.Err != nil || len(routing.Recipients) != 0 {
		t.Fatalf("unexpected routing=%#v", routing)
	}
}

func TestExecutionCapability_workerAdmission(t *testing.T) {
	interp, sess, _ := newSubmitContractInterpreter(t)
	scopes := make(chan runtime.SessionExecution, 1)
	results := make(chan runtime.ExecutionEvent, 2)
	mustSubmit(t, interp.Schedule(func() {
		submission := interp.AdmitCommand("probe", parseSourceStatement(t, "work"))
		execution := submission.Context.Session.CreateExecution()
		execution.Subscribe(func(event runtime.ExecutionEvent) { results <- event; submission.Close() })
		scopes <- execution
	}))
	execution := <-scopes
	admitted := make(chan error, 1)
	go func() { admitted <- execution.Execute(providerBroadcast("work")) }()
	if err := <-admitted; err != nil {
		t.Fatal(err)
	}
	receiveSubmitCommand(t, sess.executed)
	interp.Snapshot()
	assertProviderEvent(t, results)
	if sess.maxActive.Load() != 1 {
		t.Fatal("worker admission bypassed serialized Session execution")
	}
}

func TestExecutionCapability_shutdownDrainsAdmittedDelivery(t *testing.T) {
	interp, sess, _ := newSubmitContractInterpreter(t)
	configureNativeSendDelivery(t, sess, "direct")
	execute := sess.execute
	entered, released := make(chan struct{}), make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(released) }) }
	t.Cleanup(release)
	sess.execute = func(command session.Command, observer session.Observer) {
		close(entered)
		<-released
		execute(command, observer)
	}
	submissions := make(chan *CommandSubmission, 1)
	mustSubmit(t, interp.Schedule(func() {
		statement := parseSourceStatement(t, "@ada work")
		submission := interp.AdmitCommand("send", statement)
		submissions <- submission
		execution := submission.Context.Session.CreateExecution()
		execution.Subscribe(func(event runtime.ExecutionEvent) {
			result := deriveAdmissionEventCompletion(statement.Raw, event)
			submission.QueueCompletion(func() { publishAdmissionTestCompletion(submission, result) })
		})
		command := session.SendToParticipantCommand{Plan: submission.Context.Session.CreateParticipantSendPlan(testAlias("ada")), Message: "work", Notice: "@ada: work"}
		if err := execution.Execute(command); err != nil {
			t.Error(err)
		}
	}))
	<-entered
	interp.executor.requestClose()
	release()
	interp.Close()
	assertNativeSendRecords(t, interp.Snapshot().Room.Records, "direct")
	submission := <-submissions
	submission.session.mu.Lock()
	retained := len(submission.session.executions)
	submission.session.mu.Unlock()
	if retained != 0 {
		t.Fatal("shutdown retained execution scopes")
	}
}
