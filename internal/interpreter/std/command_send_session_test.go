package std_test

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/roomscript/coderoom/internal/agent"
	"github.com/roomscript/coderoom/internal/agent/echo"
	roomconfig "github.com/roomscript/coderoom/internal/config"
	"github.com/roomscript/coderoom/internal/interpreter/runtime"
	"github.com/roomscript/coderoom/internal/interpreter/std"
	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/session"
)

// These adapters deliberately violate snapshot/event collection ordering. A
// replacement snapshot fails staging; a stale original snapshot reaches Session's
// final membership guard. Neither path can deliver to the replacement.
func TestSendLifecycle_RejectsReplacementDespiteUncollectedDeparture(t *testing.T) {
	for _, stale := range []bool{false, true} {
		t.Run(map[bool]string{false: "replacement snapshot", true: "stale original snapshot"}[stale], func(t *testing.T) {
			host, invocation, replacement := prepareReplacementSendCase(t, stale)
			lifecycle := runtime.NewInvocationLifecycle(invocation, runtime.Context{Participants: host, Session: host})
			t.Cleanup(func() { lifecycle.Close(errors.New("cleanup")) })
			completion := make(chan runtime.CommandResult, 1)
			err := lifecycle.Start(func(result runtime.CommandResult) { completion <- result })
			if err == nil {
				t.Fatal("retained send accepted replacement")
			}
			if stale {
				if host.executions != 1 {
					t.Fatal("stale snapshot did not exercise final Session guard")
				}
				routing := assertPrimarySessionRejection(t, host, err, invocation.Staging().WaitFor[0])
				assertRejectedSendCompletion(t, lifecycle, receiveSendCompletion(t, completion), routing.Err)
			} else if host.executions != 0 || len(host.routing) != 0 || len(completion) != 0 {
				t.Fatal("replacement snapshot admitted delivery or queued an execution result")
			}
			assertNoReplacementSendDelivery(t, host, replacement)
		})
	}
}

func assertNoReplacementSendDelivery(t *testing.T, host *sendSessionHost, replacement *replacementSendAgent) {
	t.Helper()
	host.subscriptions.mu.Lock()
	subscriptions := len(host.subscriptions.handlers)
	host.subscriptions.mu.Unlock()
	if subscriptions != 0 {
		t.Errorf("rejected send retained %d subscriptions", subscriptions)
	}
	select {
	case text := <-replacement.sent:
		t.Errorf("replacement received retained message %q", text)
	default:
	}
}

func prepareReplacementSendCase(t *testing.T, stale bool) (*sendSessionHost, runtime.Invocation, *replacementSendAgent) {
	t.Helper()
	gate := &sendDepartureGate{entered: make(chan struct{}), release: make(chan struct{})}
	host := &sendSessionHost{ready: make(chan struct{}, 2), routing: make(chan session.RoutingResult, 1)}
	replacement := &replacementSendAgent{Client: echo.New(), sent: make(chan string, 1)}
	first := true
	host.Session = session.New(session.WithObserver(gate), session.WithObserver(host), session.WithAgentFactory(func(*session.Session, roomconfig.ParticipantConfig, session.AgentBackend) agent.Agent {
		if first {
			first = false
			return echo.New()
		}
		return replacement
	}))
	t.Cleanup(host.Shutdown)
	t.Cleanup(gate.unblock)
	executeSendTestCommand(t, host.Session, session.InviteCommand{Alias: "ada"})
	waitForSendSessionSignal(t, host.ready)
	invocation, err := (std.SendCommand{}).Prepare(parseSendStatement(t, "@ada work"), runtime.Context{Participants: host, Session: host})
	if err != nil {
		t.Fatal(err)
	}
	if stale {
		host.snapshot = host.Session.Participants()
	}
	host.beforeRead = func() {
		executeSendTestCommand(t, host.Session, session.RemoveCommand{Alias: invocation.Staging().WaitFor[0]})
		waitForSendSessionSignal(t, gate.entered)
		executeSendTestCommand(t, host.Session, session.InviteCommand{Alias: "ada"})
		waitForSendSessionSignal(t, host.ready)
	}
	return host, invocation, replacement
}

func assertPrimarySessionRejection(t *testing.T, host *sendSessionHost, err error, alias participant.Alias) session.RoutingResult {
	t.Helper()
	var routing session.RoutingResult
	select {
	case routing = <-host.routing: // Execute emits RoutingCompleted before returning.
	default:
		t.Fatal("Session execution did not report a routing outcome")
	}
	if routing.Kind != session.RoutingParticipantSend || !errors.Is(err, routing.Err) {
		t.Fatalf("Start did not report Session's routing rejection: %#v, %v", routing, err)
	}
	if len(routing.Recipients) != 1 || routing.Recipients[0].Alias != alias ||
		routing.Recipients[0].Role != session.RecipientPrimary || routing.Recipients[0].Status != session.DeliveryFailed ||
		routing.Recipients[0].Err == nil {
		t.Fatalf("primary routing result = %#v", routing.Recipients)
	}
	return routing
}

func assertRejectedSendCompletion(t *testing.T, lifecycle *runtime.InvocationLifecycle, result runtime.CommandResult, routingError error) {
	t.Helper()
	if !errors.Is(result.Err, routingError) || len(result.Records) != 0 {
		t.Fatalf("rejection completion = %#v; want Session error and no input record", result)
	}
	if _, accepted := lifecycle.Complete(result); accepted {
		t.Error("completion from failed Start was accepted")
	}
}

// This adapter forwards events on arrival, but its read can overtake an event
// blocked in an earlier observer. It intentionally does not satisfy the production
// reader contract. Execute stays on the test goroutine for the ready snapshot.
type sendSessionHost struct {
	*session.Session
	subscriptions sendDelivery
	ready         chan struct{}
	beforeRead    func()
	snapshot      []participant.View
	executions    int
	routing       chan session.RoutingResult
}

func (h *sendSessionHost) Execute(command session.Command) error {
	h.executions++
	if err := h.Session.Execute(command); err != nil {
		return fmt.Errorf("execute test session command: %w", err)
	}
	return nil
}

func (h *sendSessionHost) Subscribe(handle func(session.Event)) func() {
	return h.subscriptions.Subscribe(handle)
}

func (h *sendSessionHost) Participants() []participant.View {
	if h.beforeRead != nil {
		hook := h.beforeRead
		h.beforeRead = nil
		hook()
	}
	if h.snapshot != nil {
		return h.snapshot
	}
	return h.Session.Participants()
}

// This negative fixture deliberately violates the atomic boundary, preserving
// the stale snapshot/final Session guard cases independently of the real API.
func (h *sendSessionHost) SubscribeParticipants(handle func(session.Event)) ([]participant.View, func()) {
	unsubscribe := h.Subscribe(handle)
	return h.Participants(), unsubscribe
}

func (h *sendSessionHost) OnEvent(event session.Event) {
	h.subscriptions.mu.Lock()
	h.subscriptions.queue = append(h.subscriptions.queue, event)
	h.subscriptions.mu.Unlock()
	h.subscriptions.deliverEvents()
	if routed, ok := event.(session.RoutingCompleted); ok {
		h.routing <- routed.Result
	}
	if _, ok := event.(session.AgentReady); ok {
		h.ready <- struct{}{}
	}
}

type sendDepartureGate struct {
	entered   chan struct{}
	release   chan struct{}
	enterOnce sync.Once
	once      sync.Once
}

func (g *sendDepartureGate) OnEvent(event session.Event) {
	if _, ok := event.(session.AgentStopped); ok {
		g.enterOnce.Do(func() { close(g.entered) })
		<-g.release
	}
}

func (g *sendDepartureGate) unblock() { g.once.Do(func() { close(g.release) }) }

type replacementSendAgent struct {
	*echo.Client
	sent chan string
}

func (a *replacementSendAgent) Send(text string) (agent.StreamID, error) {
	a.sent <- text
	id, err := a.Client.Send(text)
	if err != nil {
		return "", fmt.Errorf("send to replacement: %w", err)
	}
	return id, nil
}

func executeSendTestCommand(t *testing.T, s *session.Session, command session.Command) {
	t.Helper()
	if err := s.Execute(command); err != nil {
		t.Fatal(err)
	}
}

func waitForSendSessionSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for session lifecycle event")
	}
}
