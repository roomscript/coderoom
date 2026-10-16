package interpreter

import (
	"errors"
	"slices"
	"testing"

	"github.com/roomscript/coderoom/internal/interpreter/runtime"
	"github.com/roomscript/coderoom/internal/interpreter/std"
	"github.com/roomscript/coderoom/internal/session"
)

func newBlockedCommandStage(t *testing.T) (*Interpreter, *lifecycleSession, *lifecycleAgent, chan Event) {
	t.Helper()
	interp, sess, backend, events, alias := newLifecycleInterpreter(t)
	if _, err := sess.Execute(session.SendToParticipantCommand{Plan: sess.CreateParticipantSendPlan(alias), Message: "busy"}); err != nil {
		t.Fatal(err)
	}
	submitLifecycleSend(t, interp, events)
	return interp, sess, backend, events
}

func assertBlockedCommandStage(t *testing.T, interp *Interpreter) *StagedSubmission {
	t.Helper()
	stage := interp.Snapshot().Stage
	if stage == nil || stage.Raw != "@ada work" || stage.Phase != StagePhasePending {
		t.Fatalf("stage = %#v", stage)
	}
	if !slices.Equal(stage.Routing, []string{"ada"}) || !slices.Equal(stage.NotReadyAliases, []string{"ada"}) || !slices.Equal(stage.Interruptible, []string{"ada"}) {
		t.Fatalf("stage readiness = %#v", stage)
	}
	return stage
}

func TestStage_snapshotAndAdmission(t *testing.T) {
	interp, _, _, events := newBlockedCommandStage(t)
	stage := assertBlockedCommandStage(t, interp)
	stage.Raw, stage.Routing[0], stage.NotReadyAliases[0], stage.Interruptible[0] = "changed", "changed", "changed", "changed"
	assertBlockedCommandStage(t, interp)
	for _, raw := range []string{"@ada next", "hello", "/who", "/shell echo work", "/invite", "/missing"} {
		if err := interp.Submit(raw); err != nil {
			t.Fatal(err)
		}
		awaitLifecycleEvent(t, events, func(event Event) bool {
			rejected, ok := event.(InputRejected)
			if !ok || rejected.Raw != raw {
				return false
			}
			if rejected.Code != ErrorStagePending || !errors.Is(rejected.Err, ErrStagePending) {
				t.Fatalf("rejection = %#v", rejected)
			}
			return true
		})
	}
	assertBlockedCommandStage(t, interp)
}

func TestStage_editAndDiscard(t *testing.T) {
	for _, edit := range []bool{false, true} {
		t.Run(map[bool]string{false: "discard", true: "edit"}[edit], func(t *testing.T) {
			interp, sess, backend, _ := newBlockedCommandStage(t)
			execution := captureStageExecution(t, interp)
			removeStageForTest(t, interp, edit)
			if interp.Snapshot().Stage != nil || sess.subscriptions.Load() != 0 {
				t.Fatal("stage resources retained")
			}
			if interp.DiscardStage() || interp.InterruptAndDispatchStage() {
				t.Fatal("operation found removed stage")
			}
			if err := execution.Execute(session.SendToParticipantCommand{}); err == nil {
				t.Fatal("discard retained native admission")
			}
			backend.unblock()
			interp.Close()
			assertLifecycleSendCount(t, interp, 0)
			if len(interp.invocations) != 0 {
				t.Fatal("removed stage retained invocation")
			}
		})
	}
}

func TestStage_editAndResubmitHasNoStaleDelivery(t *testing.T) {
	interp, _, backend, events := newBlockedCommandStage(t)
	raw, ok := interp.TakeStageForEdit()
	if !ok {
		t.Fatal("edit failed")
	}
	if err := interp.Submit(raw); err != nil {
		t.Fatal(err)
	}
	awaitLifecycleEvent(t, events, func(event Event) bool { accepted, ok := event.(SubmissionSucceeded); return ok && accepted.Raw == raw })
	backend.unblock()
	awaitLifecycleEvent(t, events, func(event Event) bool {
		dispatched, ok := event.(StagedInputDispatched)
		return ok && dispatched.Raw == raw
	})
	interp.Close()
	assertLifecycleSendCount(t, interp, 1)
}

func TestStage_legacyCoexistence(t *testing.T) {
	interp, sess, _, events, alias := newLifecycleInterpreter(t)
	// Keep broadcast on fallback to prove coexistence with an unmigrated stage.
	interp.commands = runtime.NewRegistry()
	if err := interp.commands.Register(std.SendCommand{}); err != nil {
		t.Fatal(err)
	}

	if _, err := sess.Execute(session.SendToParticipantCommand{Plan: sess.CreateParticipantSendPlan(alias), Message: "busy"}); err != nil {
		t.Fatal(err)
	}
	if err := interp.Submit("hello"); err != nil {
		t.Fatal(err)
	}
	awaitLifecycleEvent(t, events, func(event Event) bool {
		accepted, ok := event.(SubmissionSucceeded)
		return ok && accepted.Raw == "hello"
	})
	if err := interp.Submit("@ada work"); err != nil {
		t.Fatal(err)
	}
	awaitLifecycleEvent(t, events, func(event Event) bool {
		rejected, ok := event.(InputRejected)
		return ok && rejected.Code == ErrorStagePending
	})
	if stage := interp.Snapshot().Stage; stage == nil || stage.Raw != "hello" {
		t.Fatalf("legacy stage replaced: %#v", stage)
	}
	if sess.subscriptions.Load() != 0 {
		t.Fatal("rejected command prepared a lifecycle")
	}
	assertLegacyStageEdit(t, interp)
}

func TestStage_interruptRetryWaitsForActualReadiness(t *testing.T) {
	interp, sess, backend, events := newBlockedCommandStage(t)
	backend.interruptFailure.Store(true)
	if !interp.InterruptAndDispatchStage() {
		t.Fatal("first interrupt rejected")
	}
	awaitLifecycleFailure(t, events)
	stage := assertBlockedCommandStage(t, interp)
	if !stage.InterruptRequested || backend.interrupts.Load() != 1 {
		t.Fatal("failed interruption was not retained for retry")
	}
	backend.interruptFailure.Store(false)
	if !interp.InterruptAndDispatchStage() {
		t.Fatal("retry rejected")
	}
	assertAcknowledgedStage(t, interp)
	if interp.InterruptAndDispatchStage() || backend.interrupts.Load() != 2 {
		t.Fatal("successful cancellation repeated")
	}
	assertLifecycleSendCount(t, interp, 0)
	backend.unblock()
	awaitLifecycleEvent(t, events, func(event Event) bool { _, ok := event.(StagedInputDispatched); return ok })
	interp.Close()
	assertLifecycleSendCount(t, interp, 1)
	if sess.subscriptions.Load() != 0 {
		t.Fatal("interrupted stage retained subscription")
	}
}

func TestStage_shutdownOperations(t *testing.T) {
	interp, sess, _, _ := newBlockedCommandStage(t)
	interp.Close()
	if raw, ok := interp.TakeStageForEdit(); raw != "" || ok {
		t.Fatalf("closed edit = %q, %v", raw, ok)
	}
	if interp.DiscardStage() || interp.InterruptAndDispatchStage() || interp.Snapshot().Stage != nil {
		t.Fatal("closed stage still available")
	}
	if sess.subscriptions.Load() != 0 {
		t.Fatal("closed stage retained subscriptions")
	}
}

func TestStage_observerPublishesPendingAndRemoval(t *testing.T) {
	interp, sess, backend, events, alias := newLifecycleInterpreter(t)
	if _, err := sess.Execute(session.SendToParticipantCommand{Plan: sess.CreateParticipantSendPlan(alias), Message: "busy"}); err != nil {
		t.Fatal(err)
	}
	if err := interp.Submit("@ada work"); err != nil {
		t.Fatal(err)
	}
	awaitLifecycleEvent(t, events, func(event Event) bool {
		changed, ok := event.(StateChanged)
		return ok && changed.Snapshot.Stage != nil && slices.Equal(changed.Snapshot.Stage.NotReadyAliases, []string{"ada"})
	})
	assertBlockedCommandStage(t, interp)
	if err := interp.Submit("/debugview"); err != nil {
		t.Fatal(err)
	}
	awaitLifecycleEvent(t, events, func(event Event) bool { _, ok := event.(DebugRequested); return ok })
	backend.unblock()
	awaitLifecycleEvent(t, events, func(event Event) bool {
		changed, ok := event.(StateChanged)
		return ok && changed.Snapshot.Stage == nil
	})
	interp.Close()
	assertLifecycleSendCount(t, interp, 1)
}

func TestStage_acceptedDiscardResolvesBeforeShutdown(t *testing.T) {
	interp, _, _, _ := newBlockedCommandStage(t)
	entered, release := make(chan struct{}), make(chan struct{})
	if err := interp.fallback.Schedule(func() { close(entered); <-release }); err != nil {
		t.Fatal(err)
	}
	<-entered
	result := make(chan stageOperationResult, 1)
	if err := interp.fallback.Schedule(func() { result <- interp.discardStage() }); err != nil {
		t.Fatal(err)
	}
	closed := make(chan struct{})
	go func() { interp.Close(); close(closed) }()
	close(release)
	if value := <-result; !value.ok {
		t.Fatal("accepted discard did not resolve")
	}
	<-closed
	if interp.Snapshot().Stage != nil {
		t.Fatal("shutdown retained a discarded stage")
	}
}

func captureStageExecution(t *testing.T, interp *Interpreter) runtime.SessionExecution {
	t.Helper()
	captured := make(chan runtime.SessionExecution, 1)
	if err := interp.fallback.Schedule(func() { captured <- interp.invocations[interp.stage.id].submission.Context.Session.CreateExecution() }); err != nil {
		t.Fatal(err)
	}
	return <-captured
}

func assertStartupStage(t *testing.T, interp *Interpreter) {
	t.Helper()
	stage := interp.Snapshot().Stage
	if stage == nil || !slices.Equal(stage.NotReadyAliases, []string{"ada"}) || len(stage.Interruptible) != 0 || interp.InterruptAndDispatchStage() {
		t.Fatalf("startup stage = %#v", stage)
	}
}

func removeStageForTest(t *testing.T, interp *Interpreter, edit bool) {
	t.Helper()
	if edit {
		raw, ok := interp.TakeStageForEdit()
		if !ok || raw != "@ada work" {
			t.Fatalf("edit = %q, %v", raw, ok)
		}
		return
	}
	if !interp.DiscardStage() {
		t.Fatal("discard did not remove waiting work")
	}
}

func assertLegacyStageEdit(t *testing.T, interp *Interpreter) {
	t.Helper()
	raw, ok := interp.TakeStageForEdit()
	if !ok || raw != "hello" || interp.Snapshot().Stage != nil {
		t.Fatalf("legacy edit = %q, %v", raw, ok)
	}
}

func assertAcknowledgedStage(t *testing.T, interp *Interpreter) {
	t.Helper()
	stage := interp.Snapshot().Stage
	if stage == nil || len(stage.Interruptible) != 0 || !slices.Equal(stage.NotReadyAliases, []string{"ada"}) {
		t.Fatalf("acknowledgement incorrectly implied readiness: %#v", stage)
	}
}
