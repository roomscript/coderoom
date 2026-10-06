package interpreter

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/roomscript/coderoom/internal/agent"
	"github.com/roomscript/coderoom/internal/agent/echo"
	roomconfig "github.com/roomscript/coderoom/internal/config"
	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/session"
)

// Hold the actual startup goroutine after it publishes Idle but before it
// marks SessionReady and publishes AgentStarted.
type startupIdleGate struct {
	entered chan struct{}
	release chan struct{}
}

func (g startupIdleGate) OnEvent(event session.Event) {
	status, ok := event.(session.ParticipantStatusChanged)
	if !ok || status.From != participant.StatusStarting || status.To != participant.StatusIdle {
		return
	}
	close(g.entered)
	<-g.release
}

type startupDeliveryAgent struct {
	*echo.Client
	sent chan string
}

func (a *startupDeliveryAgent) Send(text string) (agent.StreamID, error) {
	a.sent <- text
	id, err := a.Client.Send(text)
	if err != nil {
		return "", fmt.Errorf("send startup test message: %w", err)
	}
	return id, nil
}

func TestSubmit_planningDuringIdleBeforeSessionReadyWaitsForStartup(t *testing.T) {
	for _, raw := range []string{"@ben hello", "hello"} {
		t.Run(raw, func(t *testing.T) {
			gate := startupIdleGate{entered: make(chan struct{}), release: make(chan struct{})}
			backend := &startupDeliveryAgent{Client: echo.New(), sent: make(chan string, 1)}
			sess := session.New(session.WithObserver(gate), session.WithAgentFactory(func(_ *session.Session, _ roomconfig.ParticipantConfig, _ session.AgentBackend) agent.Agent {
				return backend
			}))
			events := make(chan Event, 32)
			interp := New(context.Background(), sess, t.TempDir(), WithObserver(submitContractObserver{events: events}))
			t.Cleanup(interp.Close)
			var releaseOnce sync.Once
			release := func() { releaseOnce.Do(func() { close(gate.release) }) }
			t.Cleanup(release)
			receiveSubmitEvent[StateChanged](t, events)
			if err := sess.Execute(session.InviteCommand{Alias: "ben"}); err != nil {
				t.Fatal(err)
			}
			receiveSignal(t, gate.entered, "idle startup window")
			assertIdleStartupWindow(t, sess)
			assertSessionRejectsStartupDelivery(t, sess)
			mustSubmit(t, interp.Submit(raw))
			awaitStartupWindowEvent[SubmissionSucceeded](t, events)
			stage := awaitStartupWindowEvent[StateChanged](t, events).Snapshot.Stage
			if stage == nil || len(stage.Blocking) != 1 || stage.Blocking[0] != "ben" {
				t.Fatalf("stage = %#v", stage)
			}
			assertStartupDelivery(t, backend.sent, false)
			release()
			awaitStartupWindowEvent[StagedInputDispatched](t, events)
			assertStartupDelivery(t, backend.sent, true)
		})
	}
}

func assertIdleStartupWindow(t *testing.T, sess *session.Session) {
	t.Helper()
	runtime, ok := sess.Participant("ben")
	if !ok || runtime.Status != participant.StatusIdle || runtime.StartupReady {
		t.Fatalf("runtime = %#v, %v", runtime, ok)
	}
	barrier := sess.Participants()
	if len(barrier) != 1 || barrier[0].Status != participant.StatusIdle || barrier[0].StartupReady {
		t.Fatalf("planning barrier = %#v", barrier)
	}
}

// Lifecycle snapshots can arrive between submission events with a real session.
func awaitStartupWindowEvent[T Event](t *testing.T, events <-chan Event) T {
	t.Helper()
	timeout := time.After(2 * time.Second)
	for {
		select {
		case event := <-events:
			if failure, ok := event.(SubmissionFailed); ok {
				t.Fatalf("submission failed: %v", failure.Err)
			}
			if value, ok := event.(T); ok {
				return value
			}
		case <-timeout:
			t.Fatalf("timed out waiting for %T", *new(T))
			var zero T
			return zero
		}
	}
}

func assertStartupDelivery(t *testing.T, sent <-chan string, ready bool) {
	t.Helper()
	select {
	case text := <-sent:
		if !ready {
			t.Fatalf("premature delivery: %q", text)
		}
		if text != "hello" {
			t.Fatalf("delivered = %q", text)
		}
	default:
		if ready {
			t.Fatal("dispatch did not reach backend")
		}
	}
}

func assertSessionRejectsStartupDelivery(t *testing.T, sess *session.Session) {
	t.Helper()
	commands := []session.Command{
		session.PrivateSendCommand{Alias: "ben", Text: "premature"},
		session.SharedSendCommand{Plan: sess.PlanSharedSend("ben"), TextDirect: "premature"},
		session.BroadcastCommand{Aliases: []string{"ben"}, Text: "premature"},
	}
	for _, command := range commands {
		if err := sess.Execute(command); err == nil {
			t.Fatalf("%T allowed delivery before startup", command)
		}
	}
	view, _ := sess.Participant("ben")
	if view.Status != participant.StatusIdle {
		t.Fatalf("failed delivery changed status: %#v", view)
	}
}
