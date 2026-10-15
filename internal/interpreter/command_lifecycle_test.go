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
	"github.com/roomscript/coderoom/internal/interpreter/std"
	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/policy"
	"github.com/roomscript/coderoom/internal/room"
	"github.com/roomscript/coderoom/internal/session"
)

// Hold the first turn's output so the real Session supplies a busy snapshot.
type lifecycleAgent struct {
	*echo.Client
	release chan struct{}
	once    sync.Once
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

func (s *lifecycleSession) SubscribeParticipants(handle func(session.Event)) ([]participant.View, func()) {
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
	// Test-only registration: public addressed send remains on the legacy path.
	if err := interp.commands.Register(std.SendCommand{}); err != nil {
		t.Fatal(err)
	}
	if err := sess.Execute(session.InviteCommand{Alias: "ada"}); err != nil {
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
			if err := sess.Execute(session.SendToParticipantCommand{Plan: sess.CreateParticipantSendPlan(alias), Message: "busy"}); err != nil {
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
		if err := sess.Execute(session.RemoveCommand{Alias: alias}); err != nil {
			t.Fatal(err)
		}
		awaitLifecycleFailure(t, events)
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
			if failed, ok := event.(SubmissionFailed); ok {
				t.Fatalf("submission failed: %v", failed.Err)
			}
			if matches(event) {
				return
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
	if err := interp.commands.Register(std.SendCommand{}); err != nil {
		t.Fatal(err)
	}
	if err := sess.Execute(session.InviteCommand{Alias: "ada"}); err != nil {
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
			submitLifecycleSend(t, interp, events)
			awaitLifecycleDelivery(t, events, partial)
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
	if err := interp.commands.Register(std.SendCommand{}); err != nil {
		t.Fatal(err)
	}
	if err := sess.Execute(session.EnablePolicyCommand{Name: policy.SendNotices}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"ada", "ben"} {
		if err := sess.Execute(session.InviteCommand{Alias: name}); err != nil {
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
