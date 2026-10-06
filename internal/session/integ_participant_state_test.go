//go:build integration

package session_test

import (
	"testing"
	"time"

	"github.com/roomscript/coderoom/internal/agent"
	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/policy"
	"github.com/roomscript/coderoom/internal/session"
)

func TestSession_sharedSendMarksWorkingUntilFlush(t *testing.T) {
	s, events := newSessionWithCodexAgents(t, "ada")
	b := newEventBuf(events)

	if err := s.Execute(session.SharedSendCommand{
		Plan:       s.PlanSharedSend("ada"),
		TextDirect: "What is 2+2? Reply with just the number.",
	}); err != nil {
		t.Fatalf("shared send: %v", err)
	}

	waitForFirstStreamOutput(t, b, "ada", 30*time.Second)
	p, ok := s.Participant("ada")
	if !ok {
		t.Fatal("expected participant ada")
	}
	if p.Status != participant.StatusWorking {
		t.Fatalf("expected ada to be working during turn, got %q", p.Status)
	}

	waitForIdleThenFlush(t, b, "ada", 60*time.Second)
	p, _ = s.Participant("ada")
	if p.Status != participant.StatusIdle {
		t.Fatalf("expected ada to be idle after flush, got %q", p.Status)
	}
}

func TestSession_cancelInterruptEndsTurnAndReturnsIdle(t *testing.T) {
	s, events := newSessionWithCodexAgents(t, "ada")
	b := newEventBuf(events)

	if err := s.Execute(session.SharedSendCommand{
		Plan:       s.PlanSharedSend("ada"),
		TextDirect: "Write 200 numbered bullet points, one per line, with short text. Keep going until you reach 200.",
	}); err != nil {
		t.Fatalf("shared send: %v", err)
	}

	waitForFirstStreamOutputThenCancel(t, b, s, "ada", 30*time.Second)
	waitForIdleThenFlush(t, b, "ada", 60*time.Second)

	p, ok := s.Participant("ada")
	if !ok {
		t.Fatal("expected participant ada")
	}
	if p.Status != participant.StatusIdle {
		t.Fatalf("expected ada to be idle after cancellation, got %q", p.Status)
	}
}

func TestSession_privateSendMarksWorkingUntilFlush(t *testing.T) {
	s, events := newSessionWithCodexAgents(t, "ada")
	b := newEventBuf(events)

	if err := s.Execute(session.PrivateSendCommand{
		Alias: "ada",
		Text:  "What is 2+2? Reply with just the number.",
	}); err != nil {
		t.Fatalf("private send: %v", err)
	}

	waitForFirstStreamOutput(t, b, "ada", 30*time.Second)
	p, _ := s.Participant("ada")
	if p.Status != participant.StatusWorking {
		t.Fatalf("expected ada to be working during private turn, got %q", p.Status)
	}

	waitForIdleThenFlush(t, b, "ada", 60*time.Second)
	p, _ = s.Participant("ada")
	if p.Status != participant.StatusIdle {
		t.Fatalf("expected ada to be idle after flush, got %q", p.Status)
	}
}

func TestSession_broadcastMarksAllWorkingUntilFlush(t *testing.T) {
	s, events := newSessionWithCodexAgents(t, "ada", "turing")
	b := newEventBuf(events)

	if err := s.Execute(session.BroadcastCommand{
		Text: "Reply with the single word: ok",
	}); err != nil {
		t.Fatalf("broadcast: %v", err)
	}

	p, _ := s.Participant("ada")
	if p.Status != participant.StatusWorking {
		t.Fatalf("expected ada to be working during broadcast, got %q", p.Status)
	}
	p, _ = s.Participant("turing")
	if p.Status != participant.StatusWorking {
		t.Fatalf("expected turing to be working during broadcast, got %q", p.Status)
	}

	waitForFirstStreamOutput(t, b, "ada", 30*time.Second)
	waitForFirstStreamOutput(t, b, "turing", 30*time.Second)

	waitForIdleThenFlush(t, b, "ada", 60*time.Second)
	waitForIdleThenFlush(t, b, "turing", 60*time.Second)

	p, _ = s.Participant("ada")
	if p.Status != participant.StatusIdle {
		t.Fatalf("expected ada to be idle after flush, got %q", p.Status)
	}
	p, _ = s.Participant("turing")
	if p.Status != participant.StatusIdle {
		t.Fatalf("expected turing to be idle after flush, got %q", p.Status)
	}
}

func TestSession_sharedSendNoticeMarksListenerWorkingUntilFlush(t *testing.T) {
	s, events := newSessionWithCodexAgents(t, "ada", "turing")
	b := newEventBuf(events)
	if err := s.Execute(session.EnablePolicyCommand{Name: policy.SendNotices}); err != nil {
		t.Fatalf("enable send notices: %v", err)
	}

	if err := s.Execute(session.SharedSendCommand{
		Plan:          s.PlanSharedSend("ada"),
		TextDirect:    "Reply with just the word: ok",
		TextListeners: "You are a silent listener. Reply only with {\"acknowledge\":true}.",
	}); err != nil {
		t.Fatalf("shared send: %v", err)
	}

	// Listener is marked working synchronously before SendNotice is issued.
	// It may return to idle quickly (e.g., immediate compliant ack), so assert
	// only that it is never left in starting/crashed.
	p, _ := s.Participant("turing")
	if p.Status != participant.StatusWorking && p.Status != participant.StatusIdle {
		t.Fatalf("expected turing to be working or idle after notice send, got %q", p.Status)
	}

	// Listener turn is a notice; it may be silent, so wait for the notice flush.
	waitForFirstOutputFlush(t, b, "turing", 60*time.Second)
	p, _ = s.Participant("turing")
	if p.Status != participant.StatusIdle {
		t.Fatalf("expected turing to be idle after notice flush, got %q", p.Status)
	}
}

func waitForIdleThenFlush(t *testing.T, b *eventBuf, alias string, timeout time.Duration) {
	t.Helper()
	var seenIdle bool
	b.waitFor(t, timeout, func(ev session.Event) bool {
		status, ok := ev.(session.ParticipantStatusChanged)
		if ok && status.Alias == alias && status.From == participant.StatusWorking && status.To == participant.StatusIdle {
			seenIdle = true
			return false
		}
		msg, ok := ev.(session.AgentMessage)
		if !ok || msg.Alias != alias {
			return false
		}
		if _, ok := msg.Msg.Content.(agent.Output); ok && msg.Msg.Mode == agent.ModeFlush {
			// Per-item flushes can arrive before idle; only the anchor flush
			// (which triggers idle) is the definitive turn-end signal.
			if !seenIdle {
				return false
			}
			return true
		}
		return false
	})
}
