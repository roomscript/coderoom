// Package ui implements the terminal interface using Bubble Tea.
// model.go defines the application state.
package ui

import (
	"context"

	tea "charm.land/bubbletea/v2"
	"github.com/trigosec/coderoom/internal/interpreter"
	"github.com/trigosec/coderoom/internal/queue"
	"github.com/trigosec/coderoom/internal/session"
	"github.com/trigosec/coderoom/internal/ui/palette"
	"github.com/trigosec/coderoom/internal/ui/room"
	"github.com/trigosec/coderoom/internal/ui/toolbox"
)

// Option configures a Model at construction time.
type Option func(*Model)

// WithDebug enables developer debugging features (debug commands and optional
// overlays). Intended to be wired to CODEROOM_DEBUG=1 in the CLI.
func WithDebug(enabled bool) Option {
	return func(m *Model) { m.debug = enabled }
}

// WithStartupHelpTip controls whether a one-time "type /help" tip is appended
// to the transcript on first layout when the room is otherwise empty.
func WithStartupHelpTip(enabled bool) Option {
	return func(m *Model) { m.showStartupHelpTip = enabled }
}

type interpreterEventMsg struct{ event interpreter.Event }

type interpreterObserver struct {
	queue *queue.Queue[interpreter.Event]
}

func (o interpreterObserver) OnEvent(event interpreter.Event) {
	o.queue.Push(event)
}

func awaitInterpreterEvent(q *queue.Queue[interpreter.Event]) tea.Cmd {
	return func() tea.Msg {
		event, ok := q.Pull()
		if !ok {
			return nil
		}
		return interpreterEventMsg{event: event}
	}
}

// Model is the Bubble Tea application state for the coderoom TUI.
type Model struct {
	interpreter      *interpreter.Interpreter
	interpreterQueue *queue.Queue[interpreter.Event]
	room             room.Model
	toolbox          toolbox.Model
	debug            bool
	colors           map[string]string
	cwd              string
	lastSize         tea.WindowSizeMsg

	activeApprovalID           int64
	submissionPending          bool
	submissionAwaitingDispatch string
	interpreterStagePresented  bool
	preserveStageDraft         bool
	stagedDispatchRaw          string

	// showStartupHelpTip is a one-shot flag. When true, the tip will be shown on
	// the next resize/layout if the room transcript is empty, and then set to false.
	showStartupHelpTip bool
}

// New creates a Model backed by the given application context and session.
// The session must have an AgentFactory configured before any invite commands
// are executed.
func New(ctx context.Context, sess *session.Session, cwd string, opts ...Option) Model {
	interpreterQueue := queue.New[interpreter.Event]()
	interp := interpreter.New(ctx, sess, cwd)
	interp.AddObserver(interpreterObserver{queue: interpreterQueue})

	colors := make(map[string]string)
	initial := interp.Snapshot()
	for _, view := range initial.Participants {
		colors[view.Alias] = view.Color
	}
	colorByAlias := func(alias string) string { return colors[alias] }
	roomModel := room.NewPresenter(colorByAlias, palette.ColorDeparted)

	m := Model{
		interpreter:      interp,
		interpreterQueue: interpreterQueue,
		room:             roomModel,
		toolbox:          toolbox.New(),
		cwd:              cwd,
		colors:           colors,
	}
	m.toolbox, _ = m.toolbox.SetParticipants(initial.Participants)
	for _, o := range opts {
		o(&m)
	}
	return m
}

// Close stops the model-owned background queues.
func (m Model) Close() {
	m.room.Close()
	if m.interpreter != nil {
		m.interpreter.Close()
	}
	if m.interpreterQueue != nil {
		m.interpreterQueue.Close()
	}
}
