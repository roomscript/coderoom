package interpreter

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/session"
)

func TestStageOperations_takeForEditAndDiscard(t *testing.T) {
	t.Run("take returns draft and removes stage", func(t *testing.T) {
		interp, _, events := newBlockedStageInterpreter(t)

		raw, ok := interp.TakeStageForEdit()
		if !ok || raw != "hello" {
			t.Fatalf("TakeStageForEdit = %q, %v", raw, ok)
		}
		if interp.Snapshot().Stage != nil {
			t.Fatal("stage remained after take for edit")
		}
		if interp.DiscardStage() {
			t.Fatal("discard found stage removed by take")
		}
		_ = events
	})

	t.Run("discard removes stage", func(t *testing.T) {
		interp, _, _ := newBlockedStageInterpreter(t)

		if !interp.DiscardStage() {
			t.Fatal("DiscardStage returned false")
		}
		if interp.Snapshot().Stage != nil {
			t.Fatal("stage remained after discard")
		}
	})
}

func TestStageOperations_interruptDispatchesAfterCausalIdle(t *testing.T) {
	interp, sess, _ := newBlockedStageInterpreter(t)
	sess.execute = func(command session.Command, observer session.Observer) {
		cancel, ok := command.(session.CancelCommand)
		if !ok {
			return
		}
		observer.OnEvent(session.ParticipantStatusChanged{
			Alias: cancel.Alias, From: participant.StatusWorking, To: participant.StatusIdle,
		})
	}

	if !interp.InterruptAndDispatchStage() {
		t.Fatal("InterruptAndDispatchStage returned false")
	}
	if command := receiveSubmitCommand(t, sess.executed); command != (session.CancelCommand{Alias: "ada"}) {
		t.Fatalf("first command = %#v", command)
	}
	command := receiveSubmitCommand(t, sess.executed)
	if _, ok := command.(session.BroadcastCommand); !ok {
		t.Fatalf("second command = %T, want BroadcastCommand", command)
	}
	if interp.Snapshot().Stage != nil {
		t.Fatal("stage remained after interrupt-driven dispatch")
	}
	if interp.InterruptAndDispatchStage() {
		t.Fatal("duplicate interrupt found a stage")
	}
}

func TestStageOperations_duplicateInterruptDoesNotCancelTwice(t *testing.T) {
	interp, sess, _ := newBlockedStageInterpreter(t)
	if interp.Snapshot().Stage.InterruptRequested {
		t.Fatal("stage reported interrupt before request")
	}

	if !interp.InterruptAndDispatchStage() {
		t.Fatal("first interrupt returned false")
	}
	if stage := interp.Snapshot().Stage; stage == nil || !stage.InterruptRequested {
		t.Fatalf("stage after interrupt = %#v", stage)
	}
	if interp.InterruptAndDispatchStage() {
		t.Fatal("duplicate interrupt returned true")
	}
	if command := receiveSubmitCommand(t, sess.executed); command != (session.CancelCommand{Alias: "ada"}) {
		t.Fatalf("command = %#v", command)
	}
	assertNoSubmitExecution(t, sess.executed)
}

func TestStageOperations_partialCancelRetryTargetsOnlyFailures(t *testing.T) {
	interp, sess, events := newSubmitContractInterpreter(t)
	sess.barrier = []participant.Participant{
		{View: participant.View{Alias: "ada", Status: participant.StatusWorking, StartupReady: true}},
		{View: participant.View{Alias: "bob", Status: participant.StatusWorking, StartupReady: true}},
	}
	mustSubmit(t, interp.Submit("hello"))
	receiveSubmitEvent[InputAccepted](t, events)
	receiveSubmitEvent[SubmissionSucceeded](t, events)
	receiveSubmitEvent[StateChanged](t, events)

	wantErr := errors.New("cancel failed")
	sess.execute = func(command session.Command, _ session.Observer) {
		cancel, ok := command.(session.CancelCommand)
		if !ok || cancel.Alias != "ada" {
			return
		}
		sess.mu.Lock()
		sess.executeErr = wantErr
		sess.mu.Unlock()
	}
	if !interp.InterruptAndDispatchStage() {
		t.Fatal("first interrupt returned false")
	}
	if command := receiveSubmitCommand(t, sess.executed); command != (session.CancelCommand{Alias: "ada"}) {
		t.Fatalf("first command = %#v", command)
	}
	if command := receiveSubmitCommand(t, sess.executed); command != (session.CancelCommand{Alias: "bob"}) {
		t.Fatalf("second command = %#v", command)
	}
	assertInterruptRecordAliases(t, interp.Snapshot(), []string{"ada"})

	sess.mu.Lock()
	sess.executeErr = nil
	sess.mu.Unlock()
	if !interp.InterruptAndDispatchStage() {
		t.Fatal("retry returned false")
	}
	if command := receiveSubmitCommand(t, sess.executed); command != (session.CancelCommand{Alias: "bob"}) {
		t.Fatalf("retry command = %#v, want bob only", command)
	}
	assertNoSubmitExecution(t, sess.executed)
	assertInterruptRecordAliases(t, interp.Snapshot(), []string{"ada", "bob"})
	if interp.InterruptAndDispatchStage() {
		t.Fatal("completed interrupt set was issued again")
	}
}

func TestStageOperations_autoDispatchWinsBeforeEditOrDiscard(t *testing.T) {
	tests := []struct {
		name    string
		enqueue func(*interpreterExecutor, chan stageOperationResult) bool
	}{
		{name: "take for edit", enqueue: func(
			executor *interpreterExecutor,
			result chan stageOperationResult,
		) bool {
			return executor.enqueue(takeStageForEditOperation{result: result})
		}},
		{name: "discard", enqueue: func(
			executor *interpreterExecutor,
			result chan stageOperationResult,
		) bool {
			return executor.enqueue(discardStageOperation{result: result})
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			interp, sess, _ := newBlockedStageInterpreter(t)
			entered := make(chan struct{})
			release := make(chan struct{})
			interp.executor.enqueue(blockingSubmitContractOperation{entered: entered, release: release})
			receiveSignal(t, entered, "blocking operation")

			result := make(chan stageOperationResult, 1)
			if !tt.enqueue(interp.executor, result) {
				t.Fatal("enqueue stage operation")
			}
			// Recorded session events are deliberately drained before each
			// operation, even when their drain wake-up was queued afterward.
			interp.executor.recordSessionEvent(idleStageParticipantEvent())
			close(release)
			if value := receiveStageOperationResult(t, result); value.ok {
				t.Fatal("stage operation acted after auto-dispatch")
			}
			if _, ok := receiveSubmitCommand(t, sess.executed).(session.BroadcastCommand); !ok {
				t.Fatal("auto-dispatch did not execute broadcast")
			}
		})
	}
}

func TestStageOperations_shutdownOutcomes(t *testing.T) {
	sess := newSubmitContractSession()
	interp := New(context.Background(), sess, t.TempDir())
	interp.Close()

	if raw, ok := interp.TakeStageForEdit(); ok || raw != "" {
		t.Fatalf("TakeStageForEdit after shutdown = %q, %v", raw, ok)
	}
	if interp.DiscardStage() || interp.InterruptAndDispatchStage() {
		t.Fatal("stage operation succeeded after shutdown")
	}
}

func TestStageOperations_acceptedRequestResolvesBeforeShutdown(t *testing.T) {
	interp, _, _ := newBlockedStageInterpreter(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	interp.executor.enqueue(blockingSubmitContractOperation{entered: entered, release: release})
	receiveSignal(t, entered, "blocking operation")

	result := make(chan stageOperationResult, 1)
	if !interp.executor.enqueue(discardStageOperation{result: result}) {
		t.Fatal("enqueue stage operation")
	}
	closed := make(chan struct{})
	go func() {
		interp.Close()
		close(closed)
	}()
	close(release)
	if value := receiveStageOperationResult(t, result); !value.ok {
		t.Fatal("accepted stage operation did not resolve")
	}
	receiveSignal(t, closed, "interpreter shutdown")
}

func newBlockedStageInterpreter(
	t *testing.T,
) (*Interpreter, *submitContractSession, chan Event) {
	t.Helper()
	interp, sess, events := newSubmitContractInterpreter(t)
	sess.barrier = []participant.Participant{{View: participant.View{
		Alias: "ada", Status: participant.StatusWorking, StartupReady: true,
	}}}
	mustSubmit(t, interp.Submit("hello"))
	receiveSubmitEvent[InputAccepted](t, events)
	receiveSubmitEvent[SubmissionSucceeded](t, events)
	receiveSubmitEvent[StateChanged](t, events)
	return interp, sess, events
}

func idleStageParticipantEvent() session.ParticipantStatusChanged {
	return session.ParticipantStatusChanged{
		Alias: "ada", From: participant.StatusWorking, To: participant.StatusIdle,
	}
}

func receiveStageOperationResult(
	t *testing.T,
	result <-chan stageOperationResult,
) stageOperationResult {
	t.Helper()
	select {
	case value := <-result:
		return value
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for stage operation")
		return stageOperationResult{}
	}
}

func assertInterruptRecordAliases(t *testing.T, snapshot Snapshot, want []string) {
	t.Helper()
	var got []string
	for _, record := range snapshot.Room.Records {
		for _, alias := range want {
			if record.Text == "[→ "+alias+"] interrupt requested" {
				got = append(got, alias)
			}
		}
	}
	if len(got) != len(want) {
		t.Fatalf("interrupt progress records = %v, want %v; records: %#v", got, want, snapshot.Room.Records)
	}
}
