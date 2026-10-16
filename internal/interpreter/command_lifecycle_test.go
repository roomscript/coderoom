package interpreter

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/roomscript/coderoom/internal/agent"
	"github.com/roomscript/coderoom/internal/agent/echo"
	roomconfig "github.com/roomscript/coderoom/internal/config"
	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/policy"
	"github.com/roomscript/coderoom/internal/room"
	"github.com/roomscript/coderoom/internal/session"
)

// Hold the first turn's output so the real Session supplies a busy snapshot.
type lifecycleAgent struct {
	*echo.Client
	release          chan struct{}
	once             sync.Once
	interrupts       atomic.Int32
	interruptFailure atomic.Bool
}

func (a *lifecycleAgent) Read() (agent.Message, error) {
	message, err := a.Client.Read()
	if output, ok := message.Content.(agent.Output); ok && output.Text == "busy" {
		<-a.release
	}
	if err != nil {
		return message, fmt.Errorf("read lifecycle agent: %w", err)
	}
	return message, nil
}
func (a *lifecycleAgent) unblock() { a.once.Do(func() { close(a.release) }) }
func (a *lifecycleAgent) Interrupt() error {
	a.interrupts.Add(1)
	if a.interruptFailure.Load() {
		return errors.New("test interruption failed")
	}
	return nil
}

func (a *lifecycleAgent) Stop() error {
	a.unblock()
	if err := a.Client.Stop(); err != nil {
		return fmt.Errorf("stop lifecycle agent: %w", err)
	}
	return nil
}

type lifecycleSession struct {
	*session.Session
	subscriptions atomic.Int32
}

func (s *lifecycleSession) SubscribeParticipants(handle func(session.Event)) (session.ParticipantSnapshot, func()) {
	s.subscriptions.Add(1)
	initial, unsubscribe := s.Session.SubscribeParticipants(handle)
	var once sync.Once
	return initial, func() { once.Do(func() { unsubscribe(); s.subscriptions.Add(-1) }) }
}

type lifecycleReadyObserver struct{ ready chan participant.Alias }

func (o lifecycleReadyObserver) OnEvent(event session.Event) {
	if event, ok := event.(session.AgentReady); ok {
		o.ready <- event.Alias
	}
}

func newLifecycleInterpreter(t *testing.T) (*Interpreter, *lifecycleSession, *lifecycleAgent, chan Event, participant.Alias) {
	t.Helper()
	ready := make(chan participant.Alias, 4)
	backend := &lifecycleAgent{Client: echo.New(), release: make(chan struct{})}
	sess := &lifecycleSession{Session: session.New(session.WithObserver(lifecycleReadyObserver{ready}), session.WithAgentFactory(func(*session.Session, roomconfig.ParticipantConfig, session.AgentBackend) agent.Agent { return backend }))}
	events := make(chan Event, 128)
	interp := New(context.Background(), sess, t.TempDir(), WithObserver(routingObserver{events}))
	t.Cleanup(interp.Close)
	t.Cleanup(backend.unblock)
	if _, err := sess.Execute(session.InviteCommand{Alias: "ada"}); err != nil {
		t.Fatal(err)
	}
	select {
	case alias := <-ready:
		return interp, sess, backend, events, alias
	case <-time.After(2 * time.Second):
		t.Fatal("participant did not become ready")
		return nil, nil, nil, nil, participant.Alias{}
	}
}

func TestCommandLifecycle_waiting(t *testing.T) {
	for _, outcome := range []string{"ready", "departure", "close"} {
		t.Run(outcome, func(t *testing.T) {
			interp, sess, backend, events, alias := newLifecycleInterpreter(t)
			if _, err := sess.Execute(session.SendToParticipantCommand{Plan: sess.CreateParticipantSendPlan(alias), Message: "busy"}); err != nil {
				t.Fatal(err)
			}
			submitLifecycleSend(t, interp, events)
			if sess.subscriptions.Load() != 1 {
				t.Fatal("waiting invocation has no participant subscription")
			}
			assertLifecycleSendCount(t, interp, 0)
			finishWaitingLifecycle(t, interp, sess, backend, events, alias, outcome)
			if sess.subscriptions.Load() != 0 {
				t.Fatal("retained participant subscription")
			}
		})
	}
}

func finishWaitingLifecycle(t *testing.T, interp *Interpreter, sess *lifecycleSession, backend *lifecycleAgent, events <-chan Event, alias participant.Alias, outcome string) {
	t.Helper()
	switch outcome {
	case "ready":
		backend.unblock()
		awaitLifecycleEvent(t, events, isLifecycleInput)
		interp.Close()
		assertLifecycleSendCount(t, interp, 1)
	case "departure":
		if _, err := sess.Execute(session.RemoveCommand{Alias: alias}); err != nil {
			t.Fatal(err)
		}
		awaitLifecycleEvent(t, events, func(event Event) bool {
			if _, failed := event.(OperationFailed); failed {
				t.Fatal("waiting departure reported an execution failure")
			}
			discarded, ok := event.(StagedInputDiscarded)
			return ok && discarded.Raw == "@ada work"
		})
		interp.Close()
		assertLifecycleSendCount(t, interp, 0)
	case "close":
		interp.Close()
		awaitLifecycleFailure(t, events)
		assertLifecycleSendCount(t, interp, 0)
	}
}

func submitLifecycleSend(t *testing.T, interp *Interpreter, events <-chan Event) {
	t.Helper()
	if err := interp.Submit("@ada work"); err != nil {
		t.Fatal(err)
	}
	awaitLifecycleEvent(t, events, func(event Event) bool {
		accepted, ok := event.(SubmissionSucceeded)
		return ok && accepted.Raw == "@ada work"
	})
}

func awaitLifecycleEvent(t *testing.T, events <-chan Event, matches func(Event) bool) {
	t.Helper()
	timeout := time.After(2 * time.Second)
	for {
		select {
		case event := <-events:
			if matches(event) {
				return
			}
			if failed, ok := event.(SubmissionFailed); ok {
				t.Fatalf("submission failed: %v", failed.Err)
			}
		case <-timeout:
			t.Fatal("timed out waiting for lifecycle event")
			return
		}
	}
}

func awaitLifecycleFailure(t *testing.T, events <-chan Event) {
	t.Helper()
	awaitLifecycleEvent(t, events, func(event Event) bool {
		failed, ok := event.(OperationFailed)
		if !ok || failed.Raw != "@ada work" {
			return false
		}
		if failed.Err == nil {
			t.Fatalf("lifecycle failure = %v", failed.Err)
		}
		return true
	})
}

func assertLifecycleSendCount(t *testing.T, interp *Interpreter, want int) {
	t.Helper()
	var count int
	for _, record := range interp.Snapshot().Room.Records {
		if record.Text == "@ada work" {
			if !slices.Equal(record.Routing, []string{"ada"}) {
				t.Fatalf("send routing = %v", record.Routing)
			}
			count++
		}
	}
	if count != want {
		t.Fatalf("delivered input records = %d, want %d", count, want)
	}
}

// Delay the ordinary idle notification before startup readiness is committed.
type lifecycleStartupGate struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (g *lifecycleStartupGate) OnEvent(event session.Event) {
	changed, ok := event.(session.ParticipantStatusChanged)
	if !ok || changed.From != participant.StatusStarting || changed.To != participant.StatusIdle {
		return
	}
	close(g.entered)
	<-g.release
}
func (g *lifecycleStartupGate) unblock() { g.once.Do(func() { close(g.release) }) }

func TestCommandLifecycle_waitsForStartup(t *testing.T) {
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
		t.Fatal("startup did not reach idle window")
	}
	assertLifecycleStartupView(t, sess)
	submitLifecycleSend(t, interp, events)
	assertLifecycleSendCount(t, interp, 0)
	assertStartupStage(t, interp)
	if sess.subscriptions.Load() != 1 {
		t.Fatal("startup wait has no participant subscription")
	}
	gate.unblock()
	awaitLifecycleEvent(t, events, isLifecycleInput)
	interp.Close()
	assertLifecycleSendCount(t, interp, 1)
	if sess.subscriptions.Load() != 0 {
		t.Fatal("retained startup subscription")
	}
}

func isLifecycleInput(event Event) bool {
	changed, ok := event.(TranscriptChanged)
	if !ok {
		return false
	}
	return slices.ContainsFunc(changed.Delta.RecordUpdates, func(update room.IndexedRecord) bool { return update.Record.Text == "@ada work" })
}

type lifecycleNoticeAgent struct {
	*echo.Client
	failure error
}

func (a *lifecycleNoticeAgent) SendNotice(text string) (agent.StreamID, error) {
	if a.failure != nil {
		return "", a.failure
	}
	id, err := a.Client.SendNotice(text)
	if err != nil {
		return id, fmt.Errorf("send test notice: %w", err)
	}
	return id, nil
}

func TestCommandLifecycle_delivery(t *testing.T) {
	for _, partial := range []bool{false, true} {
		t.Run(fmt.Sprintf("partial=%t", partial), func(t *testing.T) {
			interp, sess, events := newNoticeLifecycleInterpreter(t, partial)
			if err := interp.Submit("@ada work"); err != nil {
				t.Fatal(err)
			}
			awaitDirectSendDelivery(t, events, partial)
			interp.Close()
			assertLifecycleDelivery(t, interp, partial)
			if sess.subscriptions.Load() != 0 {
				t.Fatal("retained delivery subscription")
			}
		})
	}
}

func newNoticeLifecycleInterpreter(t *testing.T, partial bool) (*Interpreter, *lifecycleSession, chan Event) {
	t.Helper()
	ready := make(chan participant.Alias, 4)
	sess := &lifecycleSession{Session: session.New(session.WithObserver(lifecycleReadyObserver{ready}), session.WithAgentFactory(func(_ *session.Session, config roomconfig.ParticipantConfig, _ session.AgentBackend) agent.Agent {
		backend := &lifecycleNoticeAgent{Client: echo.New()}
		if partial && config.Alias == "ben" {
			backend.failure = errors.New("notice failed")
		}
		return backend
	}))}
	events := make(chan Event, 128)
	interp := New(context.Background(), sess, t.TempDir(), WithObserver(routingObserver{events}))
	t.Cleanup(interp.Close)
	if _, err := sess.Execute(session.EnablePolicyCommand{Name: policy.SendNotices}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"ada", "ben"} {
		if _, err := sess.Execute(session.InviteCommand{Alias: name}); err != nil {
			t.Fatal(err)
		}
		select {
		case <-ready:
		case <-time.After(2 * time.Second):
			t.Fatal("notice recipient did not become ready")
		}
	}
	return interp, sess, events
}

func hasLifecycleOutput(event Event) bool {
	changed, ok := event.(TranscriptChanged)
	if !ok {
		return false
	}
	return slices.ContainsFunc(changed.Delta.RecordUpdates, func(update room.IndexedRecord) bool {
		return isLifecycleOutput(update.Record)
	})
}

func assertLifecycleDelivery(t *testing.T, interp *Interpreter, partial bool) {
	t.Helper()
	records := interp.Snapshot().Room.Records
	inputIndex := slices.IndexFunc(records, func(record room.Record) bool { return record.Text == "@ada work" })
	outputIndex := slices.IndexFunc(records, isLifecycleOutput)
	if inputIndex < 0 || outputIndex <= inputIndex {
		t.Fatalf("causal records = %#v", records)
	}
	input := records[inputIndex]
	if partial {
		if !slices.Equal(input.Routing, []string{"ada"}) || !slices.Equal(input.FailedRouting, []string{"ben"}) {
			t.Fatalf("partial routing = %#v", input)
		}
		return
	}
	if !slices.Equal(input.Routing, []string{"ada", "ben"}) {
		t.Fatalf("routing = %#v", input)
	}
}

func awaitLifecycleDelivery(t *testing.T, events <-chan Event, partial bool) {
	t.Helper()
	input, output, failure := false, false, !partial
	awaitLifecycleEvent(t, events, func(event Event) bool {
		input = input || isLifecycleInput(event)
		output = output || hasLifecycleOutput(event)
		if failed, ok := event.(OperationFailed); ok && failed.Raw == "@ada work" {
			if !partial || failed.Err == nil {
				t.Fatalf("unexpected delivery failure = %#v", failed)
			}
			failure = true
		}
		return input && output && failure
	})
}

func isLifecycleOutput(record room.Record) bool {
	if record.Msg == nil {
		return false
	}
	output, ok := record.Msg.Content.(agent.Output)
	return ok && record.Alias.Name == "ada" && output.Text == "work"
}

func assertLifecycleStartupView(t *testing.T, sess *lifecycleSession) {
	t.Helper()
	view, ok := sess.ParticipantByName("ada")
	if !ok || view.Status != participant.StatusIdle || view.StartupReady {
		t.Fatalf("startup state = %#v", view)
	}
}

// Direct delivery reports its terminal submission outcome after causal projection
// and the dispatch event; queued sends instead acknowledge before delivery.
func awaitDirectSendDelivery(t *testing.T, events <-chan Event, partial bool) {
	t.Helper()
	var publications []string
	awaitLifecycleEvent(t, events, func(event Event) bool {
		publication := directSendPublication(t, event, partial)
		if publication != "" {
			publications = append(publications, publication)
		}
		return publication == "success" || publication == "failure"
	})
	outcome := "success"
	if partial {
		outcome = "failure"
	}
	if !slices.Equal(publications, []string{"accepted", "input", "dispatch", outcome}) {
		t.Fatalf("direct send publications = %v", publications)
	}
}

func directSendPublication(t *testing.T, event Event, partial bool) string {
	t.Helper()
	switch value := event.(type) {
	case InputAccepted:
		assertSendEventRouting(t, value.Routing, false)
		return "accepted"
	case TranscriptChanged:
		if isLifecycleInput(event) {
			return "input"
		}
	case StagedInputDispatched:
		assertSendEventRouting(t, value.Routing, partial)
		return "dispatch"
	case SubmissionSucceeded:
		return "success"
	case SubmissionFailed:
		return "failure"
	case OperationFailed:
		t.Fatalf("direct send published operation failure: %#v", value)
	}
	return ""
}

func assertSendEventRouting(t *testing.T, routing []string, partial bool) {
	t.Helper()
	want := []string{"ada", "ben"}
	if partial {
		want = []string{"ada"}
	}
	if !slices.Equal(routing, want) {
		t.Fatalf("routing = %v, want %v", routing, want)
	}
}

func TestSend_unavailableTargetFailsBeforeAcceptance(t *testing.T) {
	interp, _, _, events, _ := newLifecycleInterpreter(t)
	if err := interp.Submit("@missing work"); err != nil {
		t.Fatal(err)
	}
	awaitLifecycleEvent(t, events, func(event Event) bool {
		switch value := event.(type) {
		case InputAccepted:
			t.Fatalf("unavailable send was accepted: %#v", value)
		case SubmissionFailed:
			if value.Code != ErrorParticipantUnavailable || value.Raw != "@missing work" {
				t.Fatalf("failure = %#v", value)
			}
			return true
		}
		return false
	})
	snapshot := interp.Snapshot()
	if snapshot.Stage != nil || slices.ContainsFunc(snapshot.Room.Records, func(record room.Record) bool { return record.Text == "@missing work" }) {
		t.Fatal("unavailable send retained work")
	}
}

func TestSend_queuedInputCannotOvertakeAdmittedDelivery(t *testing.T) {
	for _, next := range []string{"@ada work", "hello"} {
		t.Run(next, func(t *testing.T) {
			interp, _, backend, events, _ := newLifecycleInterpreter(t)
			entered, release := make(chan struct{}), make(chan struct{})
			if err := interp.fallback.Schedule(func() { close(entered); <-release }); err != nil {
				t.Fatal(err)
			}
			<-entered
			firstErr := interp.Submit("@ada busy")
			nextErr := interp.Submit(next)
			close(release)
			if firstErr != nil || nextErr != nil {
				t.Fatalf("submit errors: %v, %v", firstErr, nextErr)
			}
			awaitLifecycleEvent(t, events, func(event Event) bool {
				succeeded, ok := event.(SubmissionSucceeded)
				return ok && succeeded.Raw == next
			})
			stage := interp.Snapshot().Stage
			if stage == nil || stage.Raw != next {
				t.Fatalf("queued input did not wait for preceding delivery: %#v", stage)
			}
			backend.unblock()
			awaitLifecycleEvent(t, events, func(event Event) bool {
				dispatched, ok := event.(StagedInputDispatched)
				return ok && dispatched.Raw == next
			})
		})
	}
}

// Completion committed before attachment is history even if its publication is
// still held by an older observer. New commands must not wait for its replay.
func TestCommandLifecycle_completedBeforeAttachment(t *testing.T) {
	for _, held := range []bool{false, true} {
		t.Run(fmt.Sprintf("publicationHeld=%t", held), func(t *testing.T) {
			ready := make(chan participant.Alias, 4)
			completed := make(chan struct{})
			release := make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			if !held {
				unblock()
			}
			backend := &lifecycleAgent{Client: echo.New(), release: make(chan struct{})}
			sess := session.New(session.WithObserver(lifecycleReadyObserver{ready}), session.WithAgentFactory(func(*session.Session, roomconfig.ParticipantConfig, session.AgentBackend) agent.Agent { return backend }))
			t.Cleanup(sess.Shutdown)
			sess.AddObserver(&historicalCompletionObserver{completed: completed, release: release})
			alias := inviteStageRecipient(t, &lifecycleSession{Session: sess}, ready, "ada")
			if _, err := sess.Execute(session.SendToParticipantOutsideRoomCommand{Alias: alias, Text: "busy"}); err != nil {
				t.Fatal(err)
			}
			backend.unblock()
			select {
			case <-completed:
			case <-time.After(2 * time.Second):
				t.Fatal("historical work did not complete")
			}
			events := make(chan Event, 128)
			interp := New(context.Background(), sess, t.TempDir(), WithObserver(routingObserver{events}))
			t.Cleanup(interp.Close)
			t.Cleanup(unblock)
			submitLifecycleSend(t, interp, events)
			assertLifecycleSendCount(t, interp, 1)
			if interp.Snapshot().Stage != nil {
				t.Fatal("historical completion blocked new send")
			}
		})
	}
}

type historicalCompletionObserver struct {
	completed chan struct{}
	release   <-chan struct{}
	once      sync.Once
}

func (o *historicalCompletionObserver) OnEvent(event session.Event) {
	if message, ok := event.(session.AgentMessage); ok && message.WorkCompleted {
		o.once.Do(func() { close(o.completed); <-o.release })
	}
}
