package interpreter

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/roomscript/coderoom/internal/agent"
	"github.com/roomscript/coderoom/internal/agent/echo"
	roomconfig "github.com/roomscript/coderoom/internal/config"
	"github.com/roomscript/coderoom/internal/interpreter/std"
	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/promptlang"
	"github.com/roomscript/coderoom/internal/session"
	"github.com/roomscript/coderoom/internal/shell"
)

func TestLoop_publicWorkflow(t *testing.T) {
	interp, sess, events, calls := newPublicLoopInterpreter(t)
	const raw = "/loop @ada work /until /tests /max 2"
	parsed, err := promptlang.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	command, ok := interp.commands.Lookup(parsed)
	if _, loop := command.(std.LoopCommand); !ok || !loop {
		t.Fatal("loop is not publicly registered")
	}
	if err := interp.Submit(raw); err != nil {
		t.Fatal(err)
	}
	observation := &publicLoopObservation{raw: raw}
	awaitLifecycleEvent(t, events, func(event Event) bool { return observation.consume(t, event) })
	interp.Close()
	if observation.acknowledged != 1 || observation.conditions != 2 || observation.turns != 2 || calls.Load() != 2 || sess.subscriptions.Load() != 0 {
		t.Fatalf("observation=%+v calls=%d subscriptions=%d", observation, calls.Load(), sess.subscriptions.Load())
	}
	assertNoSubmissionOutcome(t, events)
}

type publicLoopObservation struct {
	raw                             string
	acknowledged, conditions, turns int
}

func (o *publicLoopObservation) consume(t *testing.T, event Event) bool {
	t.Helper()
	raw := o.raw
	switch event := event.(type) {
	case SubmissionSucceeded:
		if event.Raw == raw {
			o.acknowledged++
		}
	case ShellCompleted:
		if o.acknowledged != 1 || event.Statement.Raw != raw {
			t.Fatal("condition preceded acknowledgement or lost source")
		}
		o.conditions++
	case OperationFailed:
		t.Fatalf("loop failed: %v", event.Err)
	case LoopStatus:
		if event.Message == "[loop] turn 1/2 sent to @ada" || event.Message == "[loop] turn 2/2 sent to @ada" {
			o.turns++
		}
		return event.Message == "[loop] condition /tests succeeded"
	}
	return false
}

func newPublicLoopInterpreter(t *testing.T) (*Interpreter, *lifecycleSession, chan Event, *atomic.Int32) {
	t.Helper()
	ready := make(chan participant.Alias, 4)
	sess := &lifecycleSession{Session: session.New(session.WithObserver(lifecycleReadyObserver{ready}), session.WithAgentFactory(func(*session.Session, roomconfig.ParticipantConfig) (agent.Agent, error) {
		return echo.New(), nil
	}))}
	events := make(chan Event, 128)
	var calls atomic.Int32
	interp := New(t.Context(), sess, t.TempDir(), WithObserver(routingObserver{events}), WithShellRunner(ShellRunnerFunc(func(context.Context, string, string) shell.Result {
		if calls.Add(1) == 1 {
			return shell.Result{Status: shell.StatusFailure, Stdout: "retry evidence"}
		}
		return shell.Result{Status: shell.StatusSuccess}
	})))
	t.Cleanup(interp.Close)
	defineFacadeCommand(t, interp, events, "/def tests /shell check-tests")
	inviteStageRecipient(t, sess, ready, "ada")

	return interp, sess, events, &calls
}

// Fail the reader only after loop delivery has been acknowledged. Session then
// generates the crash status through its real participant subscription.
type crashingLoopAgent struct{ *lifecycleAgent }

func (a *crashingLoopAgent) Read() (agent.Message, error) {
	message, err := a.Client.Read()
	if output, ok := message.Content.(agent.Output); ok && output.Text == "busy" {
		<-a.release
		return agent.Message{}, errors.New("agent reader failed")
	}
	if err != nil {
		return message, fmt.Errorf("read crashing loop agent: %w", err)
	}
	return message, nil
}

func TestLoop_crashReleasesAdmission(t *testing.T) {
	interp, backend, events := newCrashingLoopInterpreter(t)
	const first = "/loop @ada busy /until /tests /max 1"
	if err := interp.Submit(first); err != nil {
		t.Fatal(err)
	}
	awaitLifecycleEvent(t, events, func(event Event) bool {
		succeeded, ok := event.(SubmissionSucceeded)
		return ok && succeeded.Raw == first
	})
	backend.unblock()
	awaitLifecycleEvent(t, events, func(event Event) bool {
		status, ok := event.(LoopStatus)
		return ok && status.Message == "[loop] stopped: participant @ada crashed"
	})
	const next = "/loop @tim work /until /tests /max 1"
	if err := interp.Submit(next); err != nil {
		t.Fatal(err)
	}
	awaitLifecycleEvent(t, events, func(event Event) bool {
		succeeded, ok := event.(SubmissionSucceeded)
		return ok && succeeded.Raw == next
	})
	awaitLifecycleEvent(t, events, func(event Event) bool {
		status, ok := event.(LoopStatus)
		return ok && status.Message == "[loop] condition /tests succeeded"
	})
}

func newCrashingLoopInterpreter(t *testing.T) (*Interpreter, *crashingLoopAgent, chan Event) {
	t.Helper()
	backend := &crashingLoopAgent{lifecycleAgent: &lifecycleAgent{Client: echo.New(), release: make(chan struct{})}}
	interp, _, events := newLoopInterpreterWithAgent(t, backend)
	t.Cleanup(backend.unblock)
	return interp, backend, events
}

// The held echo turn ends with its ordinary terminal flush when interrupted.
// This exercises CancelCommand through Session rather than injecting idle facts.
type interruptibleLoopAgent struct{ *lifecycleAgent }

func (a *interruptibleLoopAgent) Interrupt() error {
	a.interrupts.Add(1)
	a.unblock()
	return nil
}

func TestLoop_cancellationUsesOwnSessionCompletion(t *testing.T) {
	backend := &interruptibleLoopAgent{lifecycleAgent: &lifecycleAgent{Client: echo.New(), release: make(chan struct{})}}
	interp, sess, events := newLoopInterpreterWithAgent(t, backend)
	t.Cleanup(backend.unblock)
	const raw = "/loop @ada busy /until /tests /max 1"
	if err := interp.Submit(raw); err != nil {
		t.Fatal(err)
	}
	awaitLifecycleEvent(t, events, func(event Event) bool {
		succeeded, ok := event.(SubmissionSucceeded)
		return ok && succeeded.Raw == raw
	})
	before, _ := sess.ParticipantByName("ada")
	if before.Status != participant.StatusWorking || before.ActiveExecutionID == 0 {
		t.Fatal("loop work is not active")
	}
	assertUnrelatedLoopCompletion(t, interp, sess, events)
	facts := make(chan session.Event, 16)
	_, unsubscribe := sess.SubscribeParticipants(func(event session.Event) { facts <- event })
	defer unsubscribe()
	if err := interp.Submit("/cancel ada"); err != nil {
		t.Fatal(err)
	}
	assertCancelledLoopFacts(t, facts, before.Alias, before.ActiveExecutionID)
	awaitLifecycleEvent(t, events, func(event Event) bool {
		completed, ok := event.(ShellCompleted)
		return ok && completed.Statement.Raw == raw
	})
	awaitLifecycleEvent(t, events, func(event Event) bool {
		status, ok := event.(LoopStatus)
		return ok && status.Message == "[loop] condition /tests succeeded"
	})
	if backend.interrupts.Load() != 1 {
		t.Fatal("cancel did not interrupt the loop backend once")
	}
}

func assertUnrelatedLoopCompletion(t *testing.T, interp *Interpreter, sess *lifecycleSession, events <-chan Event) {
	t.Helper()
	other, _ := sess.ParticipantByName("tim")
	idle := make(chan struct{}, 1)
	_, unsubscribe := sess.SubscribeParticipants(func(event session.Event) {
		if status, ok := event.(session.ParticipantStatusChanged); ok && status.Alias == other.Alias && status.To == participant.StatusIdle {
			idle <- struct{}{}
		}
	})
	defer unsubscribe()
	if _, err := sess.Execute(session.SendToParticipantCommand{Plan: sess.CreateParticipantSendPlan(other.Alias), Message: "unrelated"}); err != nil {
		t.Fatal(err)
	}
	awaitShellSignal(t, idle)
	interp.Snapshot() // Drain the interpreter attention requested by these facts.
	assertNoShellOutcome(t, events)
}

func assertCancelledLoopFacts(t *testing.T, facts <-chan session.Event, alias participant.Alias, executionID session.ExecutionID) {
	t.Helper()
	event := awaitLoopParticipantFact(t, facts)
	completed, ok := event.(session.ParticipantWorkCompleted)
	if !ok || completed.Alias != alias || completed.ExecutionID != executionID {
		t.Fatalf("completion fact=%#v", event)
	}
	assertCancelledLoopIdle(t, awaitLoopParticipantFact(t, facts), alias)
}

func assertCancelledLoopIdle(t *testing.T, event session.Event, alias participant.Alias) {
	t.Helper()
	idle, ok := event.(session.ParticipantStatusChanged)
	if !ok || idle.Alias != alias || idle.From != participant.StatusWorking || idle.To != participant.StatusIdle || idle.ActiveExecutionID != 0 {
		t.Fatalf("idle fact=%#v", event)
	}
}

func awaitLoopParticipantFact(t *testing.T, facts <-chan session.Event) session.Event {
	t.Helper()
	select {
	case event := <-facts:
		return event
	case <-time.After(2 * time.Second):
		t.Fatal("participant fact did not arrive")
		return nil
	}
}

func newLoopInterpreterWithAgent(t *testing.T, backend agent.Agent) (*Interpreter, *lifecycleSession, chan Event) {
	t.Helper()
	ready := make(chan participant.Alias, 4)
	sess := &lifecycleSession{Session: session.New(session.WithObserver(lifecycleReadyObserver{ready}), session.WithAgentFactory(func(_ *session.Session, cfg roomconfig.ParticipantConfig) (agent.Agent, error) {
		if cfg.Alias == "ada" {
			return backend, nil
		}
		return echo.New(), nil
	}))}
	events := make(chan Event, 128)
	interp := New(t.Context(), sess, t.TempDir(), WithObserver(routingObserver{events}), WithShellRunner(ShellRunnerFunc(func(context.Context, string, string) shell.Result {
		return shell.Result{Status: shell.StatusSuccess}
	})))
	t.Cleanup(interp.Close)
	defineFacadeCommand(t, interp, events, "/def tests /shell check-tests")
	inviteStageRecipient(t, sess, ready, "ada")
	inviteStageRecipient(t, sess, ready, "tim")
	return interp, sess, events
}
