package interpreter

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/roomscript/coderoom/internal/agent"
	"github.com/roomscript/coderoom/internal/agent/echo"
	roomconfig "github.com/roomscript/coderoom/internal/config"
	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/policy"
	"github.com/roomscript/coderoom/internal/session"
)

func newMultiRecipientStage(t *testing.T) (*Interpreter, *lifecycleSession, map[string]*lifecycleAgent, chan Event) {
	t.Helper()
	agents := map[string]*lifecycleAgent{}
	for _, name := range []string{"ada", "ben", "cat"} {
		agents[name] = &lifecycleAgent{Client: echo.New(), release: make(chan struct{})}
	}
	ready := make(chan participant.Alias, 4)
	sess := &lifecycleSession{Session: session.New(session.WithObserver(lifecycleReadyObserver{ready}), session.WithAgentFactory(func(_ *session.Session, config roomconfig.ParticipantConfig, _ session.AgentBackend) agent.Agent {
		return agents[config.Alias]
	}))}
	events := make(chan Event, 128)
	interp := New(context.Background(), sess, t.TempDir(), WithObserver(routingObserver{events}))
	t.Cleanup(interp.Close)
	for _, backend := range agents {
		t.Cleanup(backend.unblock)
	}
	if err := sess.Execute(session.EnablePolicyCommand{Name: policy.SendNotices}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"ada", "ben"} {
		alias := inviteStageRecipient(t, sess, ready, name)
		if err := sess.Execute(session.SendToParticipantOutsideRoomCommand{Alias: alias, Text: "busy"}); err != nil {
			t.Fatal(err)
		}
	}
	submitLifecycleSend(t, interp, events)
	return interp, sess, agents, events
}

func inviteStageRecipient(t *testing.T, sess *lifecycleSession, ready <-chan participant.Alias, name string) participant.Alias {
	t.Helper()
	if err := sess.Execute(session.InviteCommand{Alias: name}); err != nil {
		t.Fatal(err)
	}
	select {
	case alias := <-ready:
		return alias
	case <-time.After(2 * time.Second):
		t.Fatal("recipient did not become ready")
		return participant.Alias{}
	}
}

func TestStage_partialCancellationRetriesOnlyFailures(t *testing.T) {
	interp, _, agents, events := newMultiRecipientStage(t)
	agents["ada"].interruptFailure.Store(true)
	if !interp.InterruptAndDispatchStage() {
		t.Fatal("first interrupt rejected")
	}
	awaitLifecycleFailure(t, events)
	if stage := interp.Snapshot().Stage; stage == nil || !slices.Equal(stage.Interruptible, []string{"ada"}) {
		t.Fatalf("retry candidates = %#v", stage)
	}
	agents["ada"].interruptFailure.Store(false)
	if !interp.InterruptAndDispatchStage() {
		t.Fatal("retry rejected")
	}
	if agents["ada"].interrupts.Load() != 2 || agents["ben"].interrupts.Load() != 1 {
		t.Fatal("retry repeated a successful cancellation")
	}
	if interp.InterruptAndDispatchStage() {
		t.Fatal("duplicate interrupt was accepted")
	}
	assertLifecycleSendCount(t, interp, 0)
	agents["ada"].unblock()
	agents["ben"].unblock()
	awaitLifecycleDelivery(t, events, false)
	interp.Close()
	assertLifecycleDelivery(t, interp, false)
}

func TestStage_optionalDepartureAndJoinKeepFrozenRouting(t *testing.T) {
	interp, sess, agents, events := newMultiRecipientStage(t)
	ben, _ := sess.ParticipantByName("ben")
	if err := sess.Execute(session.RemoveCommand{Alias: ben.Alias}); err != nil {
		t.Fatal(err)
	}
	awaitLifecycleEvent(t, events, func(event Event) bool {
		changed, ok := event.(StateChanged)
		return ok && changed.Snapshot.Stage != nil && slices.Equal(changed.Snapshot.Stage.Unavailable, []string{"ben"})
	})
	if err := sess.Execute(session.InviteCommand{Alias: "cat"}); err != nil {
		t.Fatal(err)
	}
	stage := interp.Snapshot().Stage
	if stage == nil || !slices.Equal(stage.Routing, []string{"ada"}) || !slices.Equal(stage.NotReadyAliases, []string{"ada"}) {
		t.Fatalf("departed or joined recipient changed retained routing: %#v", stage)
	}
	agents["ada"].unblock()
	awaitLifecycleEvent(t, events, func(event Event) bool { _, ok := event.(StagedInputDispatched); return ok })
	interp.Close()
	assertLifecycleSendCount(t, interp, 1)
	assertNoFailedStageRouting(t, interp)
}

func assertNoFailedStageRouting(t *testing.T, interp *Interpreter) {
	t.Helper()
	for _, record := range interp.Snapshot().Room.Records {
		if record.Text == "@ada work" && (len(record.FailedRouting) != 0 || len(record.UnsentRouting) != 0) {
			t.Fatalf("removed/joined recipients entered delivery: %#v", record)
		}
	}
}
