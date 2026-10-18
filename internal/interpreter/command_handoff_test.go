package interpreter

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/roomscript/coderoom/internal/agent"
	"github.com/roomscript/coderoom/internal/agent/echo"
	roomconfig "github.com/roomscript/coderoom/internal/config"
	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/room"
	"github.com/roomscript/coderoom/internal/session"
)

// Hold the completing message after Session transitions idle, before the one
// canonical observer receives it. Participant readiness can still advance.
type handoffProjectionBlocker struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (b *handoffProjectionBlocker) unblock() { b.once.Do(func() { close(b.release) }) }
func (b *handoffProjectionBlocker) OnEvent(event session.Event) {
	message, ok := event.(session.AgentMessage)
	if !ok || !message.WorkCompleted || message.Alias.Name != "ada" {
		return
	}
	close(b.entered)
	<-b.release
}

func newHandoffInterpreter(t *testing.T, blocker session.Observer, bystanders ...string) (*Interpreter, *lifecycleSession, *lifecycleAgent, chan Event, participant.Alias) {
	t.Helper()
	ready := make(chan participant.Alias, 4)
	backend := &lifecycleAgent{Client: echo.New(), release: make(chan struct{})}
	sess := &lifecycleSession{Session: session.New(session.WithObserver(lifecycleReadyObserver{ready}), session.WithAgentFactory(func(_ *session.Session, config roomconfig.ParticipantConfig) (agent.Agent, error) {
		if config.Alias == "ada" {
			return backend, nil
		}
		return echo.New(), nil
	}))}
	if blocker != nil {
		sess.AddObserver(blocker)
	}
	events := make(chan Event, 256)
	interp := New(context.Background(), sess, t.TempDir(), WithObserver(routingObserver{events}))
	t.Cleanup(interp.Close)
	t.Cleanup(backend.unblock)
	source := inviteStageRecipient(t, sess, ready, "ada")
	inviteStageRecipient(t, sess, ready, "tim")
	for _, name := range bystanders {
		inviteStageRecipient(t, sess, ready, name)
	}
	return interp, sess, backend, events, source
}

func TestHandoff_idleBeforeCanonicalCompletion(t *testing.T) {
	blocker := &handoffProjectionBlocker{entered: make(chan struct{}), release: make(chan struct{})}
	interp, sess, backend, events, source := newHandoffInterpreter(t, blocker)
	t.Cleanup(blocker.unblock)
	if _, err := sess.Execute(session.SendToParticipantOutsideRoomCommand{Alias: source, Text: "busy"}); err != nil {
		t.Fatal(err)
	}
	if err := interp.Submit("/handoff ada tim"); err != nil {
		t.Fatal(err)
	}
	awaitLifecycleEvent(t, events, func(event Event) bool { _, ok := event.(SubmissionSucceeded); return ok })
	backend.unblock()
	select {
	case <-blocker.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("source did not reach final projection barrier")
	}
	view, _ := sess.ParticipantByName("ada")
	if !view.IsReadyForWork() {
		t.Fatal("fixture did not reach idle before projection")
	}
	// Snapshot runs behind queued participant advancements, while the final message
	// remains held outside the interpreter. No old/missing source may be selected.
	stage := interp.Snapshot().Stage
	if stage == nil || !slices.Equal(stage.Routing, []string{"ada", "tim"}) {
		t.Fatalf("source wait=%#v", stage)
	}
	assertHandoffInputCount(t, interp, 0)
	blocker.unblock()
	awaitLifecycleEvent(t, events, func(event Event) bool { _, ok := event.(StagedInputDispatched); return ok })
	snapshot := interp.Snapshot()
	if snapshot.Stage != nil || sess.subscriptions.Load() != 0 {
		t.Fatal("completed handoff retained resources")
	}
	assertHandoffInputCount(t, interp, 1)
	assertHandoffRecordOrder(t, snapshot.Room.Records)
}

func assertHandoffRecordOrder(t *testing.T, records []room.Record) {
	t.Helper()
	inputIndex, previewIndex := -1, -1
	for index, record := range records {
		if record.Kind == room.KindUserInput && record.Text == "/handoff ada tim" {
			inputIndex = index
		}
		if record.Kind == room.KindSystem && strings.Contains(record.Text, "[handoff ada -> tim]") {
			previewIndex = index
			if !strings.Contains(record.Text, "busy") {
				t.Fatal("handoff did not read newly projected output")
			}
		}
	}
	if inputIndex < 0 || previewIndex <= inputIndex {
		t.Fatal("handoff preview preceded input")
	}
}

func assertHandoffInputCount(t *testing.T, interp *Interpreter, want int) {
	t.Helper()
	count := 0
	for _, record := range interp.Snapshot().Room.Records {
		if record.Kind == room.KindUserInput && record.Text == "/handoff ada tim" {
			count++
		}
	}
	if count != want {
		t.Fatalf("handoff input count=%d, want %d", count, want)
	}
}

func TestHandoff_waitingStageOperations(t *testing.T) {
	for _, action := range []string{"edit", "discard", "interrupt", "source departure", "destination departure", "close"} {
		t.Run(action, func(t *testing.T) {
			interp, sess, backend, events, source := newHandoffInterpreter(t, nil)
			if _, err := sess.Execute(session.SendToParticipantOutsideRoomCommand{Alias: source, Text: "busy"}); err != nil {
				t.Fatal(err)
			}
			if err := interp.Submit("/handoff ada tim"); err != nil {
				t.Fatal(err)
			}
			awaitLifecycleEvent(t, events, func(event Event) bool { _, ok := event.(SubmissionSucceeded); return ok })
			if stage := interp.Snapshot().Stage; stage == nil || !slices.Equal(stage.Interruptible, []string{"ada"}) {
				t.Fatalf("waiting stage=%#v", stage)
			}
			delivered := applyHandoffStageAction(t, interp, sess, backend, events, source, action)

			backend.unblock()
			interp.Close()
			if interp.Snapshot().Stage != nil || sess.subscriptions.Load() != 0 {
				t.Fatal("handoff retained stage subscriptions")
			}
			expected := 0
			if delivered {
				expected = 1
			}
			assertHandoffInputCount(t, interp, expected)
		})
	}
}

func applyHandoffStageAction(t *testing.T, interp *Interpreter, sess *lifecycleSession, backend *lifecycleAgent, events chan Event, source participant.Alias, action string) bool {
	t.Helper()
	delivered := false
	switch action {
	case "edit":
		raw, ok := interp.TakeStageForEdit()
		if !ok || raw != "/handoff ada tim" {
			t.Fatalf("edit=%q,%v", raw, ok)
		}
	case "discard":
		if !interp.DiscardStage() {
			t.Fatal("discard rejected")
		}
	case "interrupt":
		interruptHandoff(t, interp, backend, events)
		delivered = true

	case "source departure", "destination departure":
		departHandoffParticipant(t, sess, events, source, action)
	case "close":
		interp.Close()
	}
	return delivered
}

func interruptHandoff(t *testing.T, interp *Interpreter, backend *lifecycleAgent, events chan Event) {
	t.Helper()
	if !interp.InterruptAndDispatchStage() || backend.interrupts.Load() != 1 {
		t.Fatal("source interruption rejected")
	}
	if interp.Snapshot().Stage == nil {
		t.Fatal("interrupt bypassed source completion")
	}
	backend.unblock()
	awaitLifecycleEvent(t, events, func(event Event) bool { _, ok := event.(StagedInputDispatched); return ok })
}

func departHandoffParticipant(t *testing.T, sess *lifecycleSession, events chan Event, source participant.Alias, action string) {
	t.Helper()
	alias := source
	if action == "destination departure" {
		view, _ := sess.ParticipantByName("tim")
		alias = view.Alias
	}
	if _, err := sess.Execute(session.RemoveCommand{Alias: alias}); err != nil {
		t.Fatal(err)
	}
	awaitLifecycleEvent(t, events, func(event Event) bool { _, ok := event.(StagedInputDiscarded); return ok })
}

func TestHandoff_stagePresentsAllPrerequisites(t *testing.T) {
	interp, sess, backend, events, source := newHandoffInterpreter(t, nil, "ben")
	if _, err := sess.Execute(session.SendToParticipantOutsideRoomCommand{Alias: source, Text: "busy"}); err != nil {
		t.Fatal(err)
	}
	if err := interp.Submit("/handoff ada tim"); err != nil {
		t.Fatal(err)
	}
	awaitLifecycleEvent(t, events, func(event Event) bool {
		if accepted, ok := event.(InputAccepted); ok {
			if !slices.Equal(accepted.Routing, []string{"ada", "tim", "ben"}) {
				t.Fatalf("accepted participants=%v", accepted.Routing)
			}
		}
		_, ok := event.(SubmissionSucceeded)
		return ok
	})
	stage := interp.Snapshot().Stage
	if stage == nil || !slices.Equal(stage.Routing, []string{"ada", "tim", "ben"}) {
		t.Fatalf("stage=%#v", stage)
	}
	backend.unblock()
	awaitLifecycleEvent(t, events, func(event Event) bool {
		if failure, ok := event.(OperationFailed); ok {
			t.Fatalf("handoff failed: %v", failure.Err)
		}
		dispatched, ok := event.(StagedInputDispatched)
		if ok && !slices.Equal(dispatched.Routing, []string{"tim"}) {
			t.Fatalf("delivered routing=%v", dispatched.Routing)
		}
		return ok
	})
}
