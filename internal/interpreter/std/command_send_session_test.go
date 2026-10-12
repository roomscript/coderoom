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

// The adapter deliberately violates ParticipantReader's ordering contract: a
// replacement appears before the original departure reaches collection. This
// proves Session's delivery guard, not correct staging initialization.
func TestSendLifecycle_SessionRejectsReplacementDespiteUncollectedDeparture(t *testing.T) {
	gate := &sendDepartureGate{entered: make(chan struct{}), release: make(chan struct{})}
	host := &sendSessionHost{ready: make(chan struct{}, 2), routing: make(chan session.RoutingResult, 1)}
	replacement := &replacementSendAgent{Client: echo.New(), sent: make(chan string, 1)}
	first := true
	host.Session = session.New(session.WithObserver(gate), session.WithObserver(host),
		session.WithAgentFactory(func(*session.Session, roomconfig.ParticipantConfig, session.AgentBackend) agent.Agent {
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
	ctx := runtime.Context{Participants: host, Session: host}
	invocation, err := (std.SendCommand{}).Prepare(parseSendStatement(t, "@ada work"), ctx)
	if err != nil {
		t.Fatal(err)
	}
	host.beforeRead = func() {
		executeSendTestCommand(t, host.Session, session.RemoveCommand{Alias: "ada"})
		waitForSendSessionSignal(t, gate.entered)
		executeSendTestCommand(t, host.Session, session.InviteCommand{Alias: "ada"})
		waitForSendSessionSignal(t, host.ready)
	}
	lifecycle := runtime.NewInvocationLifecycle(invocation, ctx)
	t.Cleanup(func() { lifecycle.Close(errors.New("cleanup")) })
	completion := make(chan runtime.CommandResult, 1)
	err = lifecycle.Start(func(result runtime.CommandResult) { completion <- result })
	if err == nil || host.executions != 1 {
		t.Fatalf("Start error = %v, Session executions = %d; want rejected execution", err, host.executions)
	}
	routing := assertPrimarySessionRejection(t, host, err)
	assertRejectedSendCompletion(t, lifecycle, receiveSendCompletion(t, completion), routing.Err)
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

func assertPrimarySessionRejection(t *testing.T, host *sendSessionHost, err error) session.RoutingResult {
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
	if len(routing.Recipients) != 1 || routing.Recipients[0].Alias != "ada" ||
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
	return h.Session.Participants()
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
