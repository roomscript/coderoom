package ui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/trigosec/coderoom/internal/interpreter"
	"github.com/trigosec/coderoom/internal/session"
	"github.com/trigosec/coderoom/internal/ui/room/history/record"
)

// makeReadyModel returns a Model that has processed one WindowSizeMsg so the
// viewport is initialised and syncViewport calls are live.
func makeReadyModel(t *testing.T) Model {
	t.Helper()
	m := New(context.Background(), newTestSession(t), ".")
	t.Cleanup(m.Close)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	return next.(Model)
}

func makeReadyModelWithHeight(t *testing.T, height int) Model {
	t.Helper()
	m := New(context.Background(), newTestSession(t), ".")
	t.Cleanup(m.Close)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: height})
	return next.(Model)
}

// newTestSession returns a bare session suitable for UI unit tests (no factory).
func newTestSession(t *testing.T) *session.Session {
	t.Helper()
	s := session.New()
	t.Cleanup(s.Shutdown)
	return s
}

func newTestModelWithSession(t *testing.T, sess *session.Session) Model {
	t.Helper()
	m := New(context.Background(), sess, ".")
	t.Cleanup(m.Close)
	return m
}

func submitThroughInterpreter(t *testing.T, m Model, raw string) Model {
	t.Helper()
	next, _ := m.submit(raw)
	if !next.submissionPending {
		return next
	}
	return processInterpreterSubmission(t, next)
}

func processInterpreterSubmission(t *testing.T, m Model) Model {
	t.Helper()
	next := m
	for {
		event, ok := next.interpreterQueue.PullTimeout(2 * time.Second)
		if !ok {
			t.Fatal("timed out waiting for interpreter submission event")
		}
		updated, _ := next.Update(interpreterEventMsg{event: event})
		next = updated.(Model)
		switch event.(type) {
		case interpreter.UnknownCommand,
			interpreter.InputRejected,
			interpreter.SubmissionSucceeded,
			interpreter.SubmissionFailed:
			return next
		}
	}
}

func consumeInterpreterStateChange(t *testing.T, m Model) Model {
	t.Helper()
	return consumeInterpreterUntil(t, m, func(event interpreter.Event) bool {
		_, ok := event.(interpreter.StateChanged)
		return ok
	})
}

func consumeInterpreterUntil(t *testing.T, m Model, done func(interpreter.Event) bool) Model {
	t.Helper()
	for {
		event, ok := m.interpreterQueue.PullTimeout(2 * time.Second)
		if !ok {
			t.Fatal("timed out waiting for interpreter event")
		}
		next, _ := m.Update(interpreterEventMsg{event: event})
		m = next.(Model)
		if done(event) {
			return m
		}
	}
}

// pushEvent sends a session event into the model via Update and returns the result.
func pushEvent(m Model, e session.Event) Model {
	m.room.SessionObserver().OnEvent(e)
	next, _ := m.Update(sessionEventMsg{event: e})
	out := next.(Model)
	if roomEventProducesUpdate(e) {
		if updated, ok := out.room.WaitObserverUpdateTimeout(2 * time.Second); ok {
			out.room = updated
		}
	}
	return out
}

func roomEventProducesUpdate(e session.Event) bool {
	switch e.(type) {
	case session.AgentStarting,
		session.AgentStarted,
		session.AgentStopped,
		session.AgentCrashed,
		session.AgentLog,
		session.AgentMessage:
		return true
	default:
		return false
	}
}

// hasRecord reports whether any record of the given kind contains text in its body.
func hasRecord(m Model, kind record.Kind, text string) bool {
	for _, r := range m.room.HistoryRecords() {
		if r.Kind != kind {
			continue
		}
		if strings.Contains(r.Text, text) {
			return true
		}
	}
	return false
}
