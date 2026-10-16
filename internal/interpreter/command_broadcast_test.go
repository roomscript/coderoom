package interpreter

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/roomscript/coderoom/internal/agent"
	"github.com/roomscript/coderoom/internal/agent/echo"
	roomconfig "github.com/roomscript/coderoom/internal/config"
	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/room"
	"github.com/roomscript/coderoom/internal/session"
)

type broadcastFailingAgent struct{ *echo.Client }

func (*broadcastFailingAgent) Send(string) (agent.StreamID, error) {
	return "", errors.New("delivery failed")
}

func TestBroadcast_directPublication(t *testing.T) {
	for _, outcome := range []string{"success", "partial", "failure", "missing outcome"} {
		t.Run(outcome, func(t *testing.T) {
			interp, sess, events, _ := newBroadcastDeliveryInterpreter(t, outcome)
			if err := interp.Submit("work"); err != nil {
				t.Fatal(err)
			}
			publications := awaitBroadcastPublications(t, events, outcome == "success" || outcome == "partial")

			want := []string{"accepted", "input", "dispatch", "success"}
			if outcome == "partial" {
				want[3] = "failure"
			}
			if outcome == "failure" || outcome == "missing outcome" {
				want = []string{"accepted", "failure"}
			}
			if !slices.Equal(publications, want) {
				t.Fatalf("publications = %v, want %v", publications, want)
			}
			interp.Close()
			assertBroadcastRecords(t, interp, outcome)
			if sess.subscriptions.Load() != 0 {
				t.Fatal("delivery retained subscriptions")
			}
		})
	}
}

// Delivery acceptance does not imply the asynchronous echo output has arrived.
// Wait for both milestones before shutdown and the causal record assertion.
func awaitBroadcastPublications(t *testing.T, events <-chan Event, expectOutput bool) []string {
	t.Helper()
	var publications []string
	output := false
	awaitLifecycleEvent(t, events, func(event Event) bool {
		output = output || isBroadcastOutput(event)
		publication := broadcastPublication(t, event)
		if publication != "" {
			publications = append(publications, publication)
		}
		return publication == "success" || publication == "failure"
	})
	if expectOutput && !output {
		awaitLifecycleEvent(t, events, isBroadcastOutput)
	}
	return publications
}

func isBroadcastOutput(event Event) bool {
	changed, ok := event.(TranscriptChanged)
	return ok && slices.ContainsFunc(changed.Delta.RecordUpdates, func(update room.IndexedRecord) bool { return isLifecycleOutput(update.Record) })
}

type broadcastMissingOutcomeSession struct{ *lifecycleSession }

func (s *broadcastMissingOutcomeSession) Execute(command session.Command) (session.ExecutionID, error) {
	if _, ok := command.(session.BroadcastCommand); ok {
		return 1, nil
	}
	id, err := s.lifecycleSession.Execute(command)
	if err != nil {
		return id, fmt.Errorf("execute test command: %w", err)
	}
	return id, nil

}

func TestBroadcast_waitingOperations(t *testing.T) {
	for _, action := range []string{"ready", "departure", "edit", "discard", "interrupt", "close"} {
		t.Run(action, func(t *testing.T) {
			interp, sess, backend, events, alias := newWaitingBroadcast(t)
			delivered := applyBroadcastStageAction(t, interp, sess, backend, events, alias, action)
			backend.unblock()
			interp.Close()
			if interp.Snapshot().Stage != nil || sess.subscriptions.Load() != 0 {
				t.Fatal("broadcast retained stage resources")
			}
			assertBroadcastSettled(t, interp, events, delivered)
		})
	}
}

func TestBroadcast_frozenRecipientsSurviveDepartureAndReplacement(t *testing.T) {
	ready := make(chan participant.Alias, 8)
	backends := map[string]*lifecycleAgent{}
	sess := &lifecycleSession{Session: session.New(session.WithObserver(lifecycleReadyObserver{ready}), session.WithAgentFactory(func(_ *session.Session, config roomconfig.ParticipantConfig, _ session.AgentBackend) agent.Agent {
		backend := &lifecycleAgent{Client: echo.New(), release: make(chan struct{})}
		backends[config.Alias] = backend
		t.Cleanup(backend.unblock)
		return backend
	}))}
	events := make(chan Event, 256)
	interp := New(context.Background(), sess, t.TempDir(), WithObserver(routingObserver{events}))
	t.Cleanup(interp.Close)
	aliases := map[string]participant.Alias{}
	for _, name := range []string{"ada", "ben"} {
		aliases[name] = inviteBroadcastParticipant(t, sess, ready, name)
		makeBroadcastParticipantBusy(t, sess, aliases[name])
	}

	if err := interp.Submit("work"); err != nil {
		t.Fatal(err)
	}
	awaitLifecycleEvent(t, events, func(event Event) bool { value, ok := event.(SubmissionSucceeded); return ok && value.Raw == "work" })
	if _, err := sess.Execute(session.RemoveCommand{Alias: aliases["ben"]}); err != nil {
		t.Fatal(err)
	}
	// The same name is a new instance; neither this replacement nor a new alias
	// may inherit the retained delivery or its readiness requirements.
	for _, name := range []string{"ben", "cara"} {
		inviteBroadcastParticipant(t, sess, ready, name)
	}

	backends["ada"].unblock()
	awaitLifecycleEvent(t, events, func(event Event) bool {
		value, ok := event.(StagedInputDispatched)
		if !ok || value.Raw != "work" {
			return false
		}
		if !slices.Equal(value.Routing, []string{"ada"}) {
			t.Fatalf("frozen routing = %v", value.Routing)
		}
		return true
	})
	interp.Close()
	if sess.subscriptions.Load() != 0 {
		t.Fatal("retained participant subscription")
	}
	assertFrozenBroadcastRecords(t, interp)
}

func TestBroadcast_waitsForStartup(t *testing.T) {
	gate := &lifecycleStartupGate{entered: make(chan struct{}), release: make(chan struct{})}
	sess := &lifecycleSession{Session: session.New(session.WithObserver(gate), session.WithAgentFactory(func(*session.Session, roomconfig.ParticipantConfig, session.AgentBackend) agent.Agent {
		return echo.New()
	}))}
	events := make(chan Event, 128)
	interp := New(context.Background(), sess, t.TempDir(), WithObserver(routingObserver{events}))
	t.Cleanup(interp.Close)
	t.Cleanup(gate.unblock)
	if _, err := sess.Execute(session.InviteCommand{Alias: "ada"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-gate.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("startup timed out")
	}
	if err := interp.Submit("work"); err != nil {
		t.Fatal(err)
	}
	awaitLifecycleEvent(t, events, func(event Event) bool { success, ok := event.(SubmissionSucceeded); return ok && success.Raw == "work" })
	assertStartupStage(t, interp)
	gate.unblock()
	awaitLifecycleEvent(t, events, func(event Event) bool {
		dispatched, ok := event.(StagedInputDispatched)
		return ok && dispatched.Raw == "work"
	})
	interp.Close()
	if sess.subscriptions.Load() != 0 {
		t.Fatal("retained startup subscription")
	}
}

func TestBroadcast_emptyRoomRejectsWithoutAcceptance(t *testing.T) {
	events := make(chan Event, 32)
	interp := New(context.Background(), session.New(), t.TempDir(), WithObserver(routingObserver{events}))
	if err := interp.Submit("work"); err != nil {
		t.Fatal(err)
	}
	interp.Close()
	failures := 0
	for len(events) > 0 {
		switch event := (<-events).(type) {
		case InputAccepted, SubmissionSucceeded:
			t.Fatalf("empty broadcast accepted: %#v", event)
		case SubmissionFailed:
			if event.Code != ErrorExecutionFailed {
				t.Fatalf("failure = %#v", event)
			}
			failures++
		}
	}
	if failures != 1 {
		t.Fatalf("failures = %d", failures)
	}
	for _, record := range interp.Snapshot().Room.Records {
		if record.Kind == room.KindUserInput {
			t.Fatal("undelivered input recorded")
		}
	}
}

func newBroadcastDeliveryInterpreter(t *testing.T, outcome string) (*Interpreter, *lifecycleSession, chan Event, *lifecycleAgent) {
	t.Helper()
	ready := make(chan participant.Alias, 4)
	backend := &lifecycleAgent{Client: echo.New(), release: make(chan struct{})}
	sess := &lifecycleSession{Session: session.New(session.WithObserver(lifecycleReadyObserver{ready}), session.WithAgentFactory(func(_ *session.Session, config roomconfig.ParticipantConfig, _ session.AgentBackend) agent.Agent {
		if outcome == "failure" || outcome == "partial" && config.Alias == "ben" {
			return &broadcastFailingAgent{echo.New()}
		}
		if config.Alias == "ada" {
			return backend
		}
		return echo.New()
	}))}
	events := make(chan Event, 256)
	var controller SessionController = sess
	if outcome == "missing outcome" {
		controller = &broadcastMissingOutcomeSession{sess}
	}
	interp := New(context.Background(), controller, t.TempDir(), WithObserver(routingObserver{events}))
	t.Cleanup(interp.Close)
	t.Cleanup(backend.unblock)
	for _, name := range []string{"ada", "ben"} {
		inviteBroadcastParticipant(t, sess, ready, name)
	}
	return interp, sess, events, backend
}

func broadcastPublication(t *testing.T, event Event) string {
	t.Helper()
	switch value := event.(type) {
	case InputAccepted:
		if !slices.Equal(value.Routing, []string{"ada", "ben"}) {
			t.Fatalf("acceptance = %#v", value)
		}
		return "accepted"
	case TranscriptChanged:
		if hasBroadcastInput(value) {
			return "input"
		}
	case StagedInputDispatched:
		return "dispatch"
	case SubmissionSucceeded:
		return "success"
	case SubmissionFailed:
		assertBroadcastFailure(t, value)
		return "failure"
	case OperationFailed:
		t.Fatalf("direct delivery repeated outcome: %#v", value)
	}
	return ""
}

func hasBroadcastInput(event TranscriptChanged) bool {
	return slices.ContainsFunc(event.Delta.RecordUpdates, func(update room.IndexedRecord) bool {
		return update.Record.Kind == room.KindUserInput && update.Record.Text == "work"
	})
}

func assertBroadcastRecords(t *testing.T, interp *Interpreter, outcome string) {
	t.Helper()
	inputs := broadcastInputRecords(interp)
	if outcome == "failure" || outcome == "missing outcome" {
		if len(inputs) != 0 {
			t.Fatalf("undelivered inputs = %#v", inputs)
		}
	} else {
		routing := []string{"ada", "ben"}
		var failed []string
		if outcome == "partial" {
			routing, failed = []string{"ada"}, []string{"ben"}
		}
		if len(inputs) != 1 || !slices.Equal(inputs[0].Routing, routing) || !slices.Equal(inputs[0].FailedRouting, failed) {
			t.Fatalf("inputs = %#v", inputs)
		}
		records := interp.Snapshot().Room.Records
		input := slices.IndexFunc(records, func(r room.Record) bool { return r.Kind == room.KindUserInput && r.Text == "work" })
		output := slices.IndexFunc(records, isLifecycleOutput)
		if output <= input {
			t.Fatalf("causal record order: input=%d, output=%d", input, output)
		}
	}
}

func broadcastInputRecords(interp *Interpreter) []room.Record {
	var inputs []room.Record
	for _, record := range interp.Snapshot().Room.Records {
		if record.Kind == room.KindUserInput && record.Text == "work" {
			inputs = append(inputs, record)
		}
	}
	return inputs
}
func inviteBroadcastParticipant(t *testing.T, sess *lifecycleSession, ready <-chan participant.Alias, name string) participant.Alias {
	t.Helper()
	if _, err := sess.Execute(session.InviteCommand{Alias: name}); err != nil {
		t.Fatal(err)
	}
	select {
	case alias := <-ready:
		return alias
	case <-time.After(2 * time.Second):
		t.Fatal("startup timed out")
		return participant.Alias{}
	}
}
func makeBroadcastParticipantBusy(t *testing.T, sess *lifecycleSession, alias participant.Alias) {
	t.Helper()
	if _, err := sess.Execute(session.SendToParticipantCommand{Plan: sess.CreateParticipantSendPlan(alias), Message: "busy"}); err != nil {
		t.Fatal(err)
	}
}

func newWaitingBroadcast(t *testing.T) (*Interpreter, *lifecycleSession, *lifecycleAgent, chan Event, participant.Alias) {
	t.Helper()
	interp, sess, backend, events, alias := newLifecycleInterpreter(t)
	makeBroadcastParticipantBusy(t, sess, alias)
	if err := interp.Submit("work"); err != nil {
		t.Fatal(err)
	}
	awaitLifecycleEvent(t, events, func(event Event) bool { success, ok := event.(SubmissionSucceeded); return ok && success.Raw == "work" })
	stage := interp.Snapshot().Stage
	if stage == nil || !slices.Equal(stage.Routing, []string{"ada"}) || !slices.Equal(stage.NotReadyAliases, []string{"ada"}) {
		t.Fatalf("stage = %#v", stage)
	}
	if sess.subscriptions.Load() != 1 {
		t.Fatal("broadcast is not using modular lifecycle")
	}
	return interp, sess, backend, events, alias
}

func applyBroadcastStageAction(t *testing.T, interp *Interpreter, sess *lifecycleSession, backend *lifecycleAgent, events chan Event, alias participant.Alias, action string) bool {
	t.Helper()
	delivered := false
	switch action {
	case "ready", "interrupt":
		resumeBroadcast(t, interp, backend, events, action)
		delivered = true
	case "departure":
		if _, err := sess.Execute(session.RemoveCommand{Alias: alias}); err != nil {
			t.Fatal(err)
		}
		awaitLifecycleEvent(t, events, func(event Event) bool {
			discarded, ok := event.(StagedInputDiscarded)
			return ok && discarded.Raw == "work"
		})
	case "edit":
		if raw, ok := interp.TakeStageForEdit(); !ok || raw != "work" {
			t.Fatalf("edit = %q, %v", raw, ok)
		}
	case "discard":
		if !interp.DiscardStage() {
			t.Fatal("discard rejected")
		}
	}
	return delivered
}

func assertBroadcastSettled(t *testing.T, interp *Interpreter, events chan Event, delivered bool) {
	t.Helper()
	count := len(broadcastInputRecords(interp))
	if count != map[bool]int{true: 1, false: 0}[delivered] {
		t.Fatalf("delivered inputs = %d", count)
	}
	for len(events) > 0 {
		switch event := (<-events).(type) {
		case SubmissionSucceeded:
			if event.Raw == "work" {
				t.Fatal("repeated submission outcome")
			}
		case SubmissionFailed:
			if event.Raw == "work" {
				t.Fatalf("repeated submission failure: %#v", event)
			}
		}
	}
}

func assertFrozenBroadcastRecords(t *testing.T, interp *Interpreter) {
	t.Helper()
	for _, record := range interp.Snapshot().Room.Records {
		if record.Kind == room.KindUserInput && record.Text == "work" && !slices.Equal(record.Routing, []string{"ada"}) {
			t.Fatalf("input = %#v", record)
		}
		if record.Msg != nil && record.Alias.Name != "ada" {
			if output, ok := record.Msg.Content.(agent.Output); ok && output.Text == "work" {
				t.Fatal("broadcast reached a replacement or new participant")
			}
		}
	}
}

func assertBroadcastFailure(t *testing.T, failure SubmissionFailed) {
	t.Helper()
	if failure.Code != ErrorExecutionFailed || failure.Err == nil || failure.Operation != "staged dispatch" {
		t.Fatalf("failure = %#v", failure)
	}
}

func resumeBroadcast(t *testing.T, interp *Interpreter, backend *lifecycleAgent, events chan Event, action string) {
	t.Helper()
	if action == "interrupt" && !interp.InterruptAndDispatchStage() {
		t.Fatal("interrupt rejected")
	}
	backend.unblock()
	awaitLifecycleEvent(t, events, func(event Event) bool {
		dispatched, ok := event.(StagedInputDispatched)
		return ok && dispatched.Raw == "work"
	})
}

func TestBroadcast_queuedPartialDeliveryDoesNotRepeatSubmission(t *testing.T) {
	interp, sess, events, backend := newBroadcastDeliveryInterpreter(t, "partial")
	view, _ := sess.ParticipantByName("ada")
	makeBroadcastParticipantBusy(t, sess, view.Alias)
	if err := interp.Submit("work"); err != nil {
		t.Fatal(err)
	}
	awaitLifecycleEvent(t, events, func(event Event) bool {
		success, ok := event.(SubmissionSucceeded)
		return ok && success.Raw == "work"
	})
	if interp.Snapshot().Stage == nil {
		t.Fatal("queued success preceded stage retention")
	}
	backend.unblock()
	var publications []string
	awaitLifecycleEvent(t, events, func(event Event) bool {
		publication := queuedBroadcastPublication(t, event)
		if publication != "" {
			publications = append(publications, publication)
		}
		return publication == "failure"
	})
	if !slices.Equal(publications, []string{"input", "dispatch", "failure"}) {
		t.Fatalf("queued publications = %v", publications)
	}
	interp.Close()
	assertBroadcastRecords(t, interp, "partial")
}

func queuedBroadcastPublication(t *testing.T, event Event) string {
	t.Helper()
	switch value := event.(type) {
	case TranscriptChanged:
		if hasBroadcastInput(value) {
			return "input"
		}
	case StagedInputDispatched:
		return "dispatch"
	case OperationFailed:
		if value.Err == nil || value.Operation != "staged dispatch" {
			t.Fatalf("failure = %#v", value)
		}
		return "failure"
	case SubmissionSucceeded, SubmissionFailed:
		t.Fatalf("repeated submission outcome: %#v", value)
	}
	return ""
}
