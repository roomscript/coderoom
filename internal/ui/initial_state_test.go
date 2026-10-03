package ui

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/trigosec/coderoom/internal/agent"
	"github.com/trigosec/coderoom/internal/interpreter"
	"github.com/trigosec/coderoom/internal/participant"
	"github.com/trigosec/coderoom/internal/session"
)

// This fixed-state session keeps a broadcast pending without external agents.
type initialStateSession struct{ observer session.Observer }

func (*initialStateSession) Execute(session.Command) error           { return nil }
func (s *initialStateSession) AddObserver(observer session.Observer) { s.observer = observer }
func (*initialStateSession) PlanSharedSend(string) session.SharedSendPlan {
	return session.SharedSendPlan{}
}
func (*initialStateSession) Participants() []participant.View {
	return []participant.View{{Alias: "ada", Status: participant.StatusWorking, StartupReady: true, Color: "#123456"}}
}
func (s *initialStateSession) Participant(alias string) (participant.View, bool) {
	return s.Participants()[0], alias == "ada"
}
func (*initialStateSession) Shutdown() {}

func TestNew_consumesEventsQueuedBeforeUIConstruction(t *testing.T) {
	sess := &initialStateSession{}
	observer := NewObserver()
	t.Cleanup(observer.Close)
	interp := interpreter.New(context.Background(), sess, ".", interpreter.WithObserver(observer))
	t.Cleanup(interp.Close)
	sess.observer.OnEvent(session.AgentStarted{Alias: "ada"})
	sess.observer.OnEvent(session.AgentMessage{Alias: "ada", Msg: agent.Message{StreamID: "output", Mode: agent.ModeStream, Content: agent.Output{Text: "existing output"}}})
	sess.observer.OnEvent(session.ApprovalRequested{ID: 7, Alias: "ada", Req: agent.ApprovalRequest{Ask: "approve existing work?", Options: []agent.ApprovalOption{agent.OptionAccept, agent.OptionDecline}}})
	if err := interp.Submit("next turn"); err != nil {
		t.Fatal(err)
	}
	initial := interp.Snapshot()
	if initial.Stage == nil || initial.Approval == nil || len(initial.Room.Records) == 0 {
		t.Fatalf("fixture state = %#v", initial)
	}

	m := New(interp, observer, ".")
	t.Cleanup(m.Close)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = next.(Model)
	m = consumeQueuedStartup(t, m)
	requireInitialPresentation(t, m, initial)

	sess.observer.OnEvent(session.AgentMessage{Alias: "ada", Msg: agent.Message{StreamID: "output", Mode: agent.ModeStream, Content: agent.Output{Text: " continued"}}})
	requireSubsequentStreamUpdate(t, m, interp.Snapshot())
}

func consumeQueuedStartup(t *testing.T, m Model) Model {
	t.Helper()
	// Startup and application events have been queued since interpreter construction.
	completed := false
	for !completed || !m.interpreterStagePresented {
		event, ok := m.interpreterQueue.PullTimeout(2 * time.Second)
		if !ok {
			t.Fatal("timed out consuming queued startup events")
		}
		m, _ = m.handleInterpreterEvent(event)
		if _, succeeded := event.(interpreter.SubmissionSucceeded); succeeded {
			completed = true
		}
	}
	return m
}

func requireInitialPresentation(t *testing.T, m Model, initial interpreter.Snapshot) {
	t.Helper()
	if !reflect.DeepEqual(m.room.HistoryRecords(), initial.Room.Records) {
		t.Fatalf("initial records = %#v, want %#v", m.room.HistoryRecords(), initial.Room.Records)
	}
	if !m.room.IsStreaming("ada") {
		t.Fatal("existing stream was not initialized")
	}
	if m.colors["ada"] != "#123456" {
		t.Fatal("existing participant color was not initialized")
	}
	if m.activeApprovalID != 7 || !strings.Contains(m.room.View(), "approve existing work?") {
		t.Fatal("existing approval was not initialized")
	}
	if !m.interpreterStagePresented || m.room.ComposeValue() != initial.Stage.Raw {
		t.Fatal("existing stage was not initialized behind approval")
	}
	if event, ok := m.interpreterQueue.PullTimeout(20 * time.Millisecond); ok {
		t.Fatalf("unexpected duplicate queued event: %T", event)
	}

}

func requireSubsequentStreamUpdate(t *testing.T, m Model, expected interpreter.Snapshot) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for !reflect.DeepEqual(m.room.HistoryRecords(), expected.Room.Records) && time.Now().Before(deadline) {
		event, ok := m.interpreterQueue.PullTimeout(time.Until(deadline))
		if !ok {
			t.Fatal("timed out waiting for subsequent stream update")
		}
		m, _ = m.handleInterpreterEvent(event)
	}
	if !reflect.DeepEqual(m.room.HistoryRecords(), expected.Room.Records) {
		t.Fatal("subsequent update duplicated or lost records")
	}
}
