package ui

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/roomscript/coderoom/internal/agent"
	roomconfig "github.com/roomscript/coderoom/internal/config"
	"github.com/roomscript/coderoom/internal/interpreter"
	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/promptlang"
	"github.com/roomscript/coderoom/internal/session"
	"github.com/roomscript/coderoom/internal/ui/room/history/record"
)

type quitTrackingAgent struct {
	*testAgent
	stopped chan struct{}
}

func (a *quitTrackingAgent) Stop() error {
	close(a.stopped)
	return a.testAgent.Stop()
}

func TestSubmit_DebugCommandUsesInterpreterDispatch(t *testing.T) {
	m := makeReadyModel(t)

	m = submitThroughInterpreter(t, m, "/debugview")

	if got := countUserInputRecords(m, "/debugview"); got != 1 {
		t.Fatalf("user input records = %d, want 1", got)
	}
	if !hasRecord(m, record.KindSystem, "debug commands disabled") {
		t.Fatalf("expected debug result; records: %v", m.room.HistoryRecords())
	}
	if _, ok := m.interpreterQueue.TryPull(); ok {
		t.Fatal("debug submission left unexpected interpreter events")
	}
}

func TestSubmit_HelpUsesNativeInterpreterMetadata(t *testing.T) {
	sess := session.New(session.WithAgentFactory(func(*session.Session, roomconfig.ParticipantConfig, session.AgentBackend) agent.Agent {
		return newTestAgent()
	}))
	t.Cleanup(sess.Shutdown)
	m := newTestModelWithSession(t, sess)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = next.(Model)
	inviteParticipant(t, sess, "ada")
	m = pumpUntilAgentsStarted(t, m, "ada")

	m = submitThroughInterpreter(t, m, "/help")

	if got := countUserInputRecords(m, "/help"); got != 1 {
		t.Fatalf("user input records = %d, want 1", got)
	}
	for _, rec := range m.room.HistoryRecords() {
		if rec.Kind == record.KindUserInput && rec.Text == "/help" && len(rec.Routing) != 0 {
			t.Fatalf("help routing = %v, want no recipients", rec.Routing)
		}
	}
	for _, text := range []string{"[help]", "/invite <alias>", "@<alias> <text>", "Ctrl+O"} {
		if !hasRecord(m, record.KindSystem, text) {
			t.Fatalf("help output missing %q; records: %v", text, m.room.HistoryRecords())
		}
	}
}

func TestSubmit_WhoUsesNativeInterpreterHandler(t *testing.T) {
	m := makeReadyModel(t)

	m = submitThroughInterpreter(t, m, "/who")

	if got := countUserInputRecords(m, "/who"); got != 1 {
		t.Fatalf("user input records = %d, want 1", got)
	}
	if !hasRecord(m, record.KindSystem, "[no agents]") {
		t.Fatalf("expected native /who result; records: %v", m.room.HistoryRecords())
	}
}

func TestSubmit_QuitStopsSessionBeforeReturningTeaQuit(t *testing.T) {
	stopped := make(chan struct{})
	sess := session.New(session.WithAgentFactory(func(*session.Session, roomconfig.ParticipantConfig, session.AgentBackend) agent.Agent {
		return &quitTrackingAgent{testAgent: newTestAgent(), stopped: stopped}
	}))
	m := newTestModelWithSession(t, sess)
	inviteParticipant(t, sess, "ada")
	m = pumpUntil(t, m, func(event session.Event) bool {
		_, started := event.(session.AgentReady)
		return started
	})
	m = m.submitToInterpreter("/quit")

	for {
		event, ok := m.interpreterQueue.PullTimeout(2 * time.Second)
		if !ok {
			t.Fatal("timed out waiting for exit request")
		}
		next, cmd := m.Update(interpreterEventMsg{event: event})
		m = next.(Model)
		if _, exit := event.(interpreter.ExitRequested); !exit {
			continue
		}
		if cmd == nil {
			t.Fatal("exit request did not return tea.Quit")
		}
		select {
		case <-stopped:
		default:
			t.Fatal("tea.Quit returned before the session stopped its agent")
		}
		if m.submissionPending {
			t.Fatal("exit request arrived before submission completion released the gate")
		}
		if got := countUserInputRecords(m, "/quit"); got != 1 {
			t.Fatalf("user input records = %d, want 1", got)
		}
		return
	}
}

func TestSubmit_InvalidInputIsRenderedWithoutLegacyFallback(t *testing.T) {
	m := makeReadyModel(t)

	m = submitThroughInterpreter(t, m, "/invite")

	if got := countUserInputRecords(m, "/invite"); got != 0 {
		t.Fatalf("user input records = %d, want 0", got)
	}
	if !hasRecord(m, record.KindSystem, "error:") {
		t.Fatalf("expected interpreter rejection; records: %v", m.room.HistoryRecords())
	}
}

func TestSubmit_ResponseDoesNotClearNewComposerDraft(t *testing.T) {
	m := makeReadyModel(t)
	m.room = m.room.SetComposeValue("/who")

	m = m.submitToInterpreter("/who")
	if got := m.room.ComposeValue(); got != "" {
		t.Fatalf("composer after enqueue = %q, want empty", got)
	}
	m.room = m.room.SetComposeValue("next draft")
	m = processInterpreterSubmission(t, m)

	if got := m.room.ComposeValue(); got != "next draft" {
		t.Fatalf("composer after response = %q, want new draft preserved", got)
	}
}

func TestSubmit_GatesSecondSubmissionUntilTerminalOutcome(t *testing.T) {
	m := makeReadyModel(t)
	m.room = m.room.SetComposeValue("/invite")
	first, firstCmd := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	m = first.(Model)
	if !m.submissionPending {
		t.Fatal("submission gate is not active before SubmitMsg delivery")
	}
	if firstCmd == nil {
		t.Fatal("first Enter did not return a submission command")
	}

	m.room = m.room.SetComposeValue("/help")
	second, secondCmd := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	m = second.(Model)
	if secondCmd != nil {
		t.Fatal("second Enter returned a command while submission was pending")
	}
	if got := m.room.ComposeValue(); got != "/help" {
		t.Fatalf("composer while gated = %q, want preserved draft", got)
	}

	firstMsg := firstCmd()
	first, _ = m.Update(firstMsg)
	m = first.(Model)
	m = processInterpreterSubmission(t, m)
	if m.submissionPending {
		t.Fatal("submission gate remained active after terminal outcome")
	}
	if got := m.room.ComposeValue(); got != "/help" {
		t.Fatalf("composer after terminal outcome = %q, want preserved draft", got)
	}
	if got := countUserInputRecords(m, "/help"); got != 0 {
		t.Fatalf("second submission records = %d, want 0", got)
	}
}

func TestSubmit_InviteCompletesBeforeFollowingWho(t *testing.T) {
	startGate := make(chan struct{})
	sess := session.New(session.WithAgentFactory(func(*session.Session, roomconfig.ParticipantConfig, session.AgentBackend) agent.Agent {
		return &gateStartAgent{testAgent: newTestAgent(), startGate: startGate}
	}))
	m := newTestModelWithSession(t, sess)
	t.Cleanup(func() { close(startGate) })

	m = submitThroughInterpreter(t, m, "/invite ada")
	p, ok := sess.Participant("ada")
	if !ok || p.Status != participant.StatusStarting {
		t.Fatalf("participant = %#v, %v; want ada starting", p, ok)
	}
	m = submitThroughInterpreter(t, m, "/who")

	if !hasRecord(m, record.KindSystem, "[agents] ada") {
		t.Fatalf("expected /who to observe ada; records: %v", m.room.HistoryRecords())
	}
}

func TestSubmit_TerminalOutcomesReleaseGateWithoutClearingDraft(t *testing.T) {
	tests := []struct {
		name  string
		event interpreter.Event
	}{
		{name: "rejected", event: interpreter.InputRejected{Raw: "/invite", Err: errors.New("rejected")}},
		{name: "unknown", event: interpreter.UnknownCommand{Raw: "/who", Name: "who"}},
		{name: "succeeded", event: interpreter.SubmissionSucceeded{Raw: "/invite ada"}},
		{name: "failed", event: interpreter.SubmissionFailed{Raw: "/invite ada", Operation: "invite", Err: errors.New("failed")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := makeReadyModel(t)
			m.submissionPending = true
			m.room = m.room.SetComposeValue("next draft")

			m, _ = m.handleInterpreterEvent(tt.event)

			if m.submissionPending {
				t.Fatal("submission gate remained active")
			}
			if got := m.room.ComposeValue(); got != "next draft" {
				t.Fatalf("composer = %q, want preserved draft", got)
			}
		})
	}
}

func TestSubmit_FallbackFailuresPreserveCommandContext(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "invite", raw: "/invite ada", want: `error: invite "ada": failed`},
		{name: "remove", raw: "/remove ada", want: `error: remove "ada": failed`},
		{name: "cancel", raw: "/cancel ada", want: `error: cancel "ada": failed`},
		{name: "policy", raw: "/policy enable send-notices", want: "error: policy: failed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := makeReadyModel(t)
			m.submissionPending = true

			statement, err := promptlang.Parse(tt.raw)
			if err != nil {
				t.Fatal(err)
			}
			m, _ = m.handleInterpreterEvent(interpreter.SubmissionFailed{
				Statement: statement,
				Raw:       tt.raw,
				Operation: tt.name,
				Err:       errors.New("failed"),
			})

			if !hasRecord(m, record.KindSystem, tt.want) {
				t.Fatalf("expected %q; records: %v", tt.want, m.room.HistoryRecords())
			}
		})
	}
}

func TestSubmit_ShutdownRejectionRestoresClearedDraft(t *testing.T) {
	m := makeReadyModel(t)
	m.interpreter.Close()

	m = m.submitToInterpreter("/who")

	if m.submissionPending {
		t.Fatal("submission gate activated after enqueue rejection")
	}
	if got := m.room.ComposeValue(); got != "/who" {
		t.Fatalf("composer = %q, want restored submission", got)
	}
}

func countUserInputRecords(m Model, text string) int {
	count := 0
	for _, item := range m.room.HistoryRecords() {
		if item.Kind == record.KindUserInput && strings.TrimSpace(item.Text) == text {
			count++
		}
	}
	return count
}
