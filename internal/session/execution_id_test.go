package session_test

import (
	"errors"
	"testing"

	"github.com/roomscript/coderoom/internal/agent"
	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/policy"
	"github.com/roomscript/coderoom/internal/session"
)

func TestExecutionID_allCommandsIncludingFailures(t *testing.T) {
	s := newSession(t)
	var previous session.ExecutionID
	for _, test := range []struct {
		name    string
		command session.Command
		fails   bool
	}{
		{"policy", session.EnablePolicyCommand{Name: policy.SendNotices}, false},
		{"missing participant", session.CancelCommand{Alias: participant.Alias{Name: "missing"}}, true},
		{"policy again", session.EnablePolicyCommand{Name: policy.SendNotices}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			id, err := s.Execute(test.command)
			if id == 0 || id <= previous || (err != nil) != test.fails {
				t.Fatalf("Execute=%d,%v; previous=%d", id, err, previous)
			}
			previous = id
		})
	}
}

func TestExecutionID_broadcastWorkSharesOrigin(t *testing.T) {
	s, observer, adapters := newIdentityTestSession(t)
	id, err := s.Execute(session.BroadcastCommand{Text: "work"})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"ada", "turing"} {
		view, _ := s.ParticipantByName(name)
		if view.ActiveExecutionID != id {
			t.Fatalf("%s provenance=%#v, execution=%d", name, view, id)
		}
	}
	// The terminal routing event occurs before Execute returns and carries the
	// same identity even though it is detached for each observer.
	routing := mustReceive[session.RoutingCompleted](t, observer.ch)
	if routing.ExecutionID != id {
		t.Fatalf("routing execution=%d, want %d", routing.ExecutionID, id)
	}
	for _, adapter := range []*mockAgent{adapters[0], adapters[2]} {
		adapter.ch <- agent.Message{StreamID: mockTurnAnchor, Mode: agent.ModeFlush, Content: agent.Output{}}
	}
	for range 2 {
		message := receiveLifecycleCompletion(t, observer.ch)
		if message.ExecutionID != id {
			t.Fatalf("completion execution=%d, want %d", message.ExecutionID, id)
		}
		view, _ := s.Participant(message.Alias)
		if completedExecution(t, s, message.Alias) != id || view.ActiveExecutionID != 0 {
			t.Fatalf("completed provenance=%#v", view)
		}
	}
}

func TestExecutionID_noticeKeepsExistingWorkOrigin(t *testing.T) {
	s, _, _ := newIdentityTestSession(t)
	if _, err := s.Execute(session.EnablePolicyCommand{Name: policy.SendNotices}); err != nil {
		t.Fatal(err)
	}
	turing := resolvedAlias(s, "turing")
	original, err := s.Execute(session.SendToParticipantOutsideRoomCommand{Alias: turing, Text: "existing work"})
	if err != nil {
		t.Fatal(err)
	}
	next, err := s.Execute(session.SendToParticipantCommand{Plan: s.CreateParticipantSendPlan(resolvedAlias(s, "ada")), Message: "new work", Notice: "additional context"})
	if err != nil {
		t.Fatal(err)
	}
	ada, _ := s.ParticipantByName("ada")
	existing, _ := s.Participant(turing)
	if next == original || ada.ActiveExecutionID != next || existing.ActiveExecutionID != original {
		t.Fatalf("executions=%d,%d; origins=%d,%d", original, next, ada.ActiveExecutionID, existing.ActiveExecutionID)
	}
}

func TestExecutionID_failedDeliveryDoesNotBecomeWorkOrigin(t *testing.T) {
	s, _, adapters := newIdentityTestSession(t)
	alias := resolvedAlias(s, "ada")
	adapters[0].sendErr = errors.New("rejected delivery")
	failed, err := s.Execute(session.SendToParticipantOutsideRoomCommand{Alias: alias, Text: "failed"})
	if err == nil || failed == 0 {
		t.Fatalf("failed execution=%d,%v", failed, err)
	}
	view, _ := s.Participant(alias)
	if view.Status != participant.StatusIdle || view.ActiveExecutionID != 0 {
		t.Fatalf("failed delivery provenance=%#v", view)
	}
	adapters[0].sendErr = nil
	accepted, err := s.Execute(session.SendToParticipantOutsideRoomCommand{Alias: alias, Text: "accepted"})
	if err != nil {
		t.Fatal(err)
	}
	view, _ = s.Participant(alias)
	if accepted <= failed || view.ActiveExecutionID != accepted {
		t.Fatalf("accepted execution=%d, origin=%d", accepted, view.ActiveExecutionID)
	}
}

func completedExecution(t *testing.T, s *session.Session, alias participant.Alias) session.ExecutionID {
	t.Helper()
	initial, unsubscribe := s.SubscribeParticipants(func(session.Event) {})
	defer unsubscribe()
	for _, view := range initial.Participants {
		if view.Alias == alias {
			return view.LastCompletedExecutionID
		}
	}
	return 0
}
