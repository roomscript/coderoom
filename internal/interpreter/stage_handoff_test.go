package interpreter

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/roomscript/coderoom/internal/interpreter/runtime"
	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/promptlang"
	"github.com/roomscript/coderoom/internal/room"
	"github.com/roomscript/coderoom/internal/session"
)

// Complete immediately on launch to exercise queued settlement as well.
type handoffInvocation struct{ staging runtime.Staging }

func (i *handoffInvocation) Staging() *runtime.Staging { return &i.staging }
func (i *handoffInvocation) Close()                    {}
func (i *handoffInvocation) Go(complete func(runtime.CommandResult)) error {
	complete(runtime.CommandResult{Records: []room.Record{{Kind: room.KindUserInput, Routing: []string{"ada"}}}})
	return nil
}

func TestStage_retainsOwnershipBeforeQueuedAdvancement(t *testing.T) {
	for _, tt := range []struct {
		name      string
		departure bool
	}{{"ready", false}, {"departure", true}} {
		t.Run(tt.name, func(t *testing.T) {
			interp, sess, backend, events, alias := newLifecycleInterpreter(t)
			if _, err := sess.Execute(session.SendToParticipantCommand{Plan: sess.CreateParticipantSendPlan(alias), Message: "busy"}); err != nil {
				t.Fatal(err)
			}
			parsed, err := promptlang.Parse("@ada work")
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			if err := interp.fallback.Schedule(func() {
				done <- retainBeforeQueuedAdvancement(interp, sess, backend, alias, parsed, tt.departure)
			}); err != nil {
				t.Fatal(err)
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			awaitLifecycleEvent(t, events, func(event Event) bool {
				if _, failed := event.(OperationFailed); failed {
					t.Fatal("handoff reported an execution failure")
				}
				if tt.departure {
					_, ok := event.(StagedInputDiscarded)
					return ok
				}
				_, ok := event.(StagedInputDispatched)
				return ok
			})
			if interp.Snapshot().Stage != nil || sess.subscriptions.Load() != 0 {
				t.Fatal("handoff retained stage or subscription")
			}
		})
	}
}

// Holds the publication lane while participant facts arrive before retention.
func retainBeforeQueuedAdvancement(interp *Interpreter, sess *lifecycleSession, backend *lifecycleAgent, alias participant.Alias, parsed promptlang.ParsedStatement, departure bool) error {
	submission := interp.fallback.AdmitCommand("send", parsed)
	invocation := &handoffInvocation{staging: runtime.Staging{WaitFor: []participant.Alias{alias}}}
	requests := make(chan struct{}, 16)
	submission.Context.EventProcessing = eventProcessingRequestFunc(func() {
		_ = interp.fallback.Schedule(func() { interp.processCommandEvents(1) })
		requests <- struct{}{}
	})
	lifecycle := runtime.NewInvocationLifecycle(invocation, submission.Context)
	interp.invocations = map[uint64]pendingInvocation{1: {lifecycle: lifecycle, publication: &invocationPublication{interpreter: interp, id: 1, submission: submission}}}
	waited, startErr := lifecycle.Start(func(result runtime.CommandResult) {
		submission.QueueCompletion(func() { interp.completeCommand(1, result) })
	})
	if startErr != nil {
		return fmt.Errorf("start handoff: %w", startErr)
	}
	if !waited {
		return errors.New("handoff did not enter waiting")
	}
	if departure {
		if _, removeErr := sess.Execute(session.RemoveCommand{Alias: alias}); removeErr != nil {
			return fmt.Errorf("remove handoff participant: %w", removeErr)
		}
	} else {
		backend.unblock()
	}
	select {
	case <-requests:
		if _, waiting := lifecycle.Readiness(); !waiting {
			return errors.New("participant callback advanced lifecycle outside owner lane")
		}
		interp.retainStage(1, parsed, invocation.Staging())
		interp.invocations[1].publication.publishSubmission(nil)
		return nil
	case <-time.After(3 * time.Second):
		return errors.New("handoff did not request advancement")
	}
}
