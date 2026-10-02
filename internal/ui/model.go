// Package ui implements the terminal interface using Bubble Tea.
// model.go defines the application state.
package ui

import (
	tea "charm.land/bubbletea/v2"
	"github.com/trigosec/coderoom/internal/interpreter"
	"github.com/trigosec/coderoom/internal/queue"
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

// Observer queues interpreter events until the UI consumes them.
// Create it before constructing the interpreter and install it with WithObserver.
type Observer struct {
	queue *queue.Queue[interpreter.Event]
}

// OnEvent queues an application event for ordered UI delivery.
func (o *Observer) OnEvent(event interpreter.Event) {
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

// New creates a presenter consuming the observer installed during interpreter construction.
// The caller owns interpreter shutdown; Close stops the UI event queue.
func New(interp *interpreter.Interpreter, observer *Observer, cwd string, opts ...Option) Model {
	colors := make(map[string]string)
	colorByAlias := func(alias string) string { return colors[alias] }
	roomModel := room.NewPresenter(colorByAlias, palette.ColorDeparted)

	m := Model{
		interpreter:      interp,
		interpreterQueue: observer.queue,
		room:             roomModel,
		toolbox:          toolbox.New(),
		cwd:              cwd,
		colors:           colors,
	}
	for _, o := range opts {
		o(&m)
	}
	return m
}

// Close stops the model-owned background queues.
func (m Model) Close() {
	if m.interpreterQueue != nil {
		m.interpreterQueue.Close()
	}
}

// NewObserver creates the queue used by a UI and its interpreter.
func NewObserver() *Observer {
	return &Observer{queue: queue.New[interpreter.Event]()}
}

// Close releases queued events and unblocks the listener.
func (o *Observer) Close() { o.queue.Close() }
