// Package ui implements the terminal interface using Bubble Tea.
// model.go defines the application state.
package ui

import (
	"errors"
	"sync"

	tea "charm.land/bubbletea/v2"
	"github.com/roomscript/coderoom/internal/interpreter"
	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/queue"
	"github.com/roomscript/coderoom/internal/ui/palette"
	"github.com/roomscript/coderoom/internal/ui/room"
	"github.com/roomscript/coderoom/internal/ui/toolbox"
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

// Observer queues interpreter events and application messages for the UI loop.
// Create it before constructing the interpreter and install it with WithObserver.
type Observer struct {
	queue  *queue.Queue[tea.Msg]
	mu     sync.Mutex
	closed bool
}

// OnEvent wraps an interpreter event for ordered UI delivery.
func (o *Observer) OnEvent(event interpreter.Event) {
	o.queue.Push(interpreterEventMsg{event: event})
}

func awaitUIMessage(q *queue.Queue[tea.Msg]) tea.Cmd {
	return func() tea.Msg {
		event, ok := q.Pull()
		if !ok {
			return nil
		}
		return event
	}
}

// Model is the Bubble Tea application state for the coderoom TUI.
type Model struct {
	interpreter *interpreter.Interpreter
	observer    *Observer
	room        room.Model
	toolbox     toolbox.Model
	debug       bool
	quitting    bool
	colors      map[participant.Alias]string
	cwd         string
	lastSize    tea.WindowSizeMsg

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
	colors := make(map[participant.Alias]string)
	colorByAlias := func(alias participant.Alias) string { return colors[alias] }
	colorByName := func(name string) string {
		for alias, color := range colors {
			if alias.Name == name {
				return color
			}
		}
		return ""
	}
	roomModel := room.NewPresenter(colorByAlias, colorByName, palette.ColorDeparted)

	m := Model{
		interpreter: interp,
		observer:    observer,
		room:        roomModel,
		toolbox:     toolbox.New(),
		cwd:         cwd,
		colors:      colors,
	}
	for _, o := range opts {
		o(&m)
	}
	return m
}

// Close stops the UI message queue.
func (m Model) Close() {
	if m.observer != nil {
		m.observer.Close()
	}
}

// NewObserver creates the queue used by a UI and its interpreter.
func NewObserver() *Observer {
	return &Observer{queue: queue.New[tea.Msg]()}
}

// Close releases queued events and unblocks the listener.
func (o *Observer) Close() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.closed = true
	o.queue.Close()
}

func (o *Observer) dispatch(message tea.Msg) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed {
		return errors.New("UI message queue is closed")
	}
	o.queue.Push(message)
	return nil
}
