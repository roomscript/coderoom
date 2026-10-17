package interpreter

import (
	"slices"
	"testing"

	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/session"
)

func TestParticipantCommands_realSessionEffectsPrecedeAcknowledgement(t *testing.T) {
	for _, operation := range []string{"cancel", "remove"} {
		t.Run(operation, func(t *testing.T) {
			interp, sess, backend, events, alias := newLifecycleInterpreter(t)
			if _, err := sess.Execute(session.SendToParticipantCommand{Plan: sess.CreateParticipantSendPlan(alias), Message: "busy"}); err != nil {
				t.Fatal(err)
			}
			raw := "/" + operation + " ada"
			if err := interp.Submit(raw); err != nil {
				t.Fatal(err)
			}
			var projected Snapshot
			awaitLifecycleEvent(t, events, func(event Event) bool {
				if changed, ok := event.(StateChanged); ok {
					projected = changed.Snapshot
				}
				if failed, ok := event.(SubmissionFailed); ok {
					t.Fatalf("submission failed: %v", failed.Err)
				}
				succeeded, ok := event.(SubmissionSucceeded)
				return ok && succeeded.Raw == raw
			})
			assertParticipantCommandEffect(t, projected, sess, backend, alias, operation)
			if operation == "remove" {
				awaitCanonicalRemoval(t, events, projected, alias)
			}
		})
	}
}

func assertParticipantCommandEffect(t *testing.T, snapshot Snapshot, sess *lifecycleSession, backend *lifecycleAgent, alias participant.Alias, operation string) {
	t.Helper()
	view, exists := sess.Participant(alias)
	if operation == "remove" {
		if exists || slices.ContainsFunc(snapshot.Participants, func(view participant.View) bool { return view.Alias == alias }) {
			t.Fatalf("removed participant retained: exists=%v participants=%v", exists, snapshot.Participants)
		}
		return
	}
	// This backend records interruption without finishing the held turn. A cancel
	// acknowledgement must not imply normal completion or an idle participant.
	if backend.interrupts.Load() != 1 || !exists || view.Status != participant.StatusWorking {
		t.Fatalf("interrupts=%d participant=%#v exists=%v", backend.interrupts.Load(), view, exists)
	}
	for _, projected := range snapshot.Participants {
		if projected.Alias == alias && projected.Status == participant.StatusWorking {
			return
		}
	}
	t.Fatal("canonical participant snapshot lost the still-running turn")
}

// Reader-owned departure publication can follow command acknowledgement. Require
// the actual published update, without a Snapshot call that could hide ordering.
func awaitCanonicalRemoval(t *testing.T, events <-chan Event, snapshot Snapshot, alias participant.Alias) {
	t.Helper()
	if snapshot.Room.Departed[alias] {
		if slices.Contains(snapshot.Room.Members, alias) {
			t.Fatal("departed participant retained as room member")
		}
		return
	}
	awaitLifecycleEvent(t, events, func(event Event) bool {
		changed, ok := event.(StateChanged)
		if !ok || !changed.Snapshot.Room.Departed[alias] {
			return false
		}
		if slices.Contains(changed.Snapshot.Room.Members, alias) {
			t.Fatal("departed participant retained as room member")
		}
		return true
	})
}
