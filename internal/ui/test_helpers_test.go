package ui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/trigosec/coderoom/internal/interpreter"
	"github.com/trigosec/coderoom/internal/queue"
	roomstate "github.com/trigosec/coderoom/internal/room"
	"github.com/trigosec/coderoom/internal/session"
	"github.com/trigosec/coderoom/internal/ui/room/history/record"
)

// makeReadyModel returns a Model that has processed one WindowSizeMsg so the
// viewport is initialised and syncViewport calls are live.
func makeReadyModel(t *testing.T) Model {
	t.Helper()
	m := New(context.Background(), newTestSession(t), ".")
	t.Cleanup(m.Close)
	t.Cleanup(func() {
		if chat := syntheticRooms[m.interpreter]; chat != nil {
			chat.Close()
			delete(syntheticRooms, m.interpreter)
		}
		delete(testApprovals, m.interpreter)
		delete(testStages, m.interpreter)
	})
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	return next.(Model)
}

func makeReadyModelWithHeight(t *testing.T, height int) Model {
	t.Helper()
	m := New(context.Background(), newTestSession(t), ".")
	t.Cleanup(m.Close)
	t.Cleanup(func() {
		if chat := syntheticRooms[m.interpreter]; chat != nil {
			chat.Close()
			delete(syntheticRooms, m.interpreter)
		}
		delete(testApprovals, m.interpreter)
		delete(testStages, m.interpreter)
	})
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
	attachSessionOracle(t, m, sess)
	t.Cleanup(m.Close)
	t.Cleanup(func() {
		if chat := syntheticRooms[m.interpreter]; chat != nil {
			chat.Close()
			delete(syntheticRooms, m.interpreter)
		}
		delete(testApprovals, m.interpreter)
		delete(testStages, m.interpreter)
	})
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

// Synthetic projection scenarios exercise the same canonical room projector
// and interpreter DTO adapter without registering a production UI observer.
var testApprovals = map[*interpreter.Interpreter]*interpreter.Approval{}
var testStages = map[*interpreter.Interpreter]*interpreter.StagedSubmission{}
var syntheticRooms = map[*interpreter.Interpreter]*roomstate.Room{}
var sessionOracles = map[*interpreter.Interpreter]*queue.Queue[session.Event]{}

type sessionOracle struct{ queue *queue.Queue[session.Event] }

func (o sessionOracle) OnEvent(event session.Event) { o.queue.Push(event) }
func attachSessionOracle(t *testing.T, m Model, sess *session.Session) {
	q := queue.New[session.Event]()
	sessionOracles[m.interpreter] = q
	sess.AddObserver(sessionOracle{queue: q})
	t.Cleanup(func() { q.Close(); delete(sessionOracles, m.interpreter) })
}
func pushEvent(m Model, event session.Event) Model {
	chat := syntheticRooms[m.interpreter]
	if chat == nil {
		chat = roomstate.New()
		syntheticRooms[m.interpreter] = chat
	}
	version := chat.Snapshot().Version
	chat.ApplyEvent(event)
	delta, err := chat.Delta(version)
	if err != nil {
		snapshot := chat.Snapshot()
		delta = roomstate.Delta{Version: snapshot.Version, Meta: roomstate.DeltaMeta{Departed: snapshot.Departed, OpenStreams: snapshot.OpenStreams}}
		for index, record := range snapshot.Records {
			delta.RecordUpdates = append(delta.RecordUpdates, roomstate.IndexedRecord{Index: index, Record: record})
		}
	}
	m, _ = m.handleInterpreterEvent(interpreter.TranscriptChanged{Delta: delta})
	switch event := event.(type) {
	case session.ApprovalRequested:
		approval := &interpreter.Approval{ID: event.ID, Alias: event.Alias, Kind: interpreter.ApprovalKind(event.Req.Kind), Prompt: event.Req.Ask}
		for _, option := range event.Req.Options {
			approval.Options = append(approval.Options, interpreter.ApprovalOption{ID: string(option)})
		}
		testApprovals[m.interpreter] = approval
		m, _ = m.presentInterpreterSnapshot(interpreter.Snapshot{Approval: approval, Stage: testStages[m.interpreter]})
	case session.ApprovalCleared:
		if event.ID == m.activeApprovalID {
			delete(testApprovals, m.interpreter)
			m, _ = m.presentInterpreterSnapshot(interpreter.Snapshot{Stage: testStages[m.interpreter]})
		}
	}
	return m
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

// Wait for observable presentation rather than assuming one snapshot per
// runtime event: the interpreter may coalesce a synchronous causal burst.
func consumeInterpreterPresentation(t *testing.T, m Model, ready func(Model) bool) Model {
	t.Helper()
	for !ready(m) {
		event, ok := m.interpreterQueue.PullTimeout(2 * time.Second)
		if !ok {
			t.Fatal("timed out waiting for interpreter presentation")
		}
		next, _ := m.Update(interpreterEventMsg{event: event})
		m = next.(Model)
	}
	return m
}
