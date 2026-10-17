package interpreter

import (
	"context"
	"sync/atomic"
	"testing"

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
	sess := &lifecycleSession{Session: session.New(session.WithObserver(lifecycleReadyObserver{ready}), session.WithAgentFactory(func(*session.Session, roomconfig.ParticipantConfig, session.AgentBackend) agent.Agent {
		return echo.New()
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
