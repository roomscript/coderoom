// Package room composes the in-room UI: history viewport + compose input.
//
// It owns focus and layout between these components, but it does not interpret
// submitted text as commands/actions. The parent UI decides what to do with a
// submission.
package room

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"

	tea "charm.land/bubbletea/v2"
	"github.com/atotto/clipboard"
	"github.com/roomscript/coderoom/internal/interpreter"
	roomstate "github.com/roomscript/coderoom/internal/room"
	"github.com/roomscript/coderoom/internal/ui/room/approval"
	"github.com/roomscript/coderoom/internal/ui/room/compose"
	"github.com/roomscript/coderoom/internal/ui/room/history"
	rec "github.com/roomscript/coderoom/internal/ui/room/history/record"
)

var (
	systemClipboardRead  = clipboard.ReadAll
	systemClipboardWrite = clipboard.WriteAll
)

type roomFocus int

const (
	focusInput roomFocus = iota
	focusHistory
)

type inputKind int

const (
	inputCompose inputKind = iota
	inputApproval
	inputStaged
)

type inputModel struct {
	kind     inputKind
	compose  compose.Model
	approval approval.Model
	staged   stagedInput
}

func (m inputModel) height(totalH int) int {
	if m.kind == inputApproval {
		// Approval view is variable; keep it to 6 lines max and at least 3.
		return min(6, max(totalH/3, 3))
	}
	return m.compose.Height()
}

type stagedInput struct {
	status string
}

type approvalState struct {
	previousInputKind inputKind
	restoreFocus      roomFocus
}

// SubmitMsg is emitted when the user submits the composer (Enter without Alt).
// The parent should handle the text (parse, route, execute) and update the room
// history accordingly.
type SubmitMsg struct {
	Text string
}

// StagedEditMsg is emitted when the user presses Esc while the composer is
// staged, returning to draft mode with the staged text loaded for editing.
type StagedEditMsg struct{}

// StagedInterruptMsg is emitted when the user presses Ctrl+X while the composer
// is staged, requesting an interrupt-and-send.
type StagedInterruptMsg struct{}

// StagedClearMsg is emitted when the user clears a staged composer (Ctrl+C).
type StagedClearMsg struct{}

// ApprovalDecisionMsg is emitted when the user confirms an approval option.
// The parent forwards this choice to the interpreter.
type ApprovalDecisionMsg struct {
	Choice interpreter.ApprovalChoice
}

// Model is the Bubble Tea component for a single room: history + composer.
type Model struct {
	projectionIndices map[int]int
	projectionVersion uint64
	presentation      roomstate.Snapshot
	history           history.Model
	input             inputModel
	approval          approvalState
	activeFocus       roomFocus
	historyLive       bool
	debug             bool
	lastSize          tea.WindowSizeMsg
	clipboardRead     func() (string, error)
	clipboardWrite    func(string) error
}

func newPresentationModel(colorByAlias func(string) string, departedColor string) Model {
	compose := compose.New()
	return Model{
		history: history.New(colorByAlias, departedColor),
		input: inputModel{
			kind:     inputCompose,
			compose:  compose,
			approval: approval.New(),
		},
		approval: approvalState{
			previousInputKind: inputCompose,
			restoreFocus:      focusInput,
		},
		activeFocus:    focusInput,
		historyLive:    true,
		clipboardRead:  systemClipboardRead,
		clipboardWrite: defaultClipboardWriter(os.Stdout),
	}
}

func defaultClipboardWriter(out io.Writer) func(string) error {
	return func(text string) error {
		if err := systemClipboardWrite(text); err == nil {
			return nil
		}
		return writeOSC52(out, text)
	}
}

func writeOSC52(out io.Writer, text string) error {
	if out == nil {
		return errors.New("missing terminal output")
	}
	encoded := base64.StdEncoding.EncodeToString([]byte(text))
	_, err := io.WriteString(out, "\x1b]52;c;"+encoded+"\a")
	if err != nil {
		return fmt.Errorf("write OSC52 escape: %w", err)
	}
	return nil
}

// Ready reports whether HandleResize has been called at least once.
func (m Model) Ready() bool { return m.history.Ready() }

// Width returns the current room inner width.
func (m Model) Width() int { return m.history.Width() }

// HistoryView renders the history viewport.
func (m Model) HistoryView() string { return m.history.View() }

// HistoryRecords returns the room's current record slice.
func (m Model) HistoryRecords() []rec.Record { return m.history.Records() }

// HistoryRenderedContent returns the raw rendered record content joined by newlines.
func (m Model) HistoryRenderedContent() string { return m.history.RenderedContent() }

// HistoryPlainText returns a plain-text transcript of the rendered records.
func (m Model) HistoryPlainText() string { return m.history.PlainText() }

// HistoryHeight returns the history viewport height.
func (m Model) HistoryHeight() int { return m.history.Height() }

// SetHistorySnapshot replaces the rendered transcript state from the room package.
func (m Model) SetHistorySnapshot(snapshot roomstate.Snapshot) Model {
	m.history = m.history.ReplaceSnapshot(snapshot)
	m = m.syncHistoryFollowAnchor()
	return m
}

// IsStreaming reports whether alias currently has an open turn.
func (m Model) IsStreaming(alias string) bool { return m.history.IsStreaming(alias) }

// IsReasoningStreaming reports whether alias currently has an open reasoning record.
func (m Model) IsReasoningStreaming(alias string) bool {
	return m.history.IsReasoningStreaming(alias)
}

// StreamingIdx returns the record index for the given streaming alias.
func (m Model) StreamingIdx(alias string) (int, bool) { return m.history.StreamingIdx(alias) }

// IsDeparted reports whether alias has left or crashed.
func (m Model) IsDeparted(alias string) bool { return m.history.IsDeparted(alias) }

// ComposeValue returns the current composer buffer.
func (m Model) ComposeValue() string { return m.input.compose.Value() }

// ComposeHeight returns the current composer height.
func (m Model) ComposeHeight() int { return m.input.compose.Height() }

// SetComposeValue replaces the composer buffer and updates layout.
func (m Model) SetComposeValue(s string) Model {
	m.input.compose = m.input.compose.SetValue(s)
	return m.syncAfterCompose()
}

// IsComposerStaged reports whether the composer is currently staged (read-only).
func (m Model) IsComposerStaged() bool { return m.input.kind == inputStaged }

// SetComposerStaged enables staged mode with the given text and status line.
// The compose buffer is shown read-only and keystrokes are blocked until the
// user exits staged mode.
func (m Model) SetComposerStaged(text string, status string) Model {
	if m.input.kind == inputApproval {
		m.approval.previousInputKind = inputStaged
	} else {
		m.input.kind = inputStaged
	}
	m.input.staged.status = status
	m.input.compose = m.input.compose.SetValue(text).Blur()
	if m.lastSize.Width > 0 && m.lastSize.Height > 0 {
		m = m.HandleResize(m.lastSize.Width, m.lastSize.Height)
	}
	return m
}

// SetComposerStagedStatus updates the staged status line without changing
// staged text.
func (m Model) SetComposerStagedStatus(status string) Model {
	if m.input.kind != inputStaged &&
		(m.input.kind != inputApproval || m.approval.previousInputKind != inputStaged) {
		return m
	}
	m.input.staged.status = status
	if m.lastSize.Width > 0 && m.lastSize.Height > 0 {
		m = m.HandleResize(m.lastSize.Width, m.lastSize.Height)
	}
	return m
}

// ClearComposerStaged exits staged mode and clears the status line.
func (m Model) ClearComposerStaged() Model {
	stagedBehindApproval := m.input.kind == inputApproval &&
		m.approval.previousInputKind == inputStaged
	if m.input.kind != inputStaged && !stagedBehindApproval {
		return m
	}
	if stagedBehindApproval {
		m.approval.previousInputKind = inputCompose
	} else {
		m.input.kind = inputCompose
	}
	m.input.staged.status = ""
	// If we blurred the textarea while staged, ensure we restore focus when the
	// input area is focused; otherwise the composer becomes uneditable after
	// auto-dispatch clears staging.
	if m.activeFocus == focusInput {
		m.input.compose, _ = m.input.compose.Focus()
	}
	if m.lastSize.Width > 0 && m.lastSize.Height > 0 {
		m = m.HandleResize(m.lastSize.Width, m.lastSize.Height)
	}
	return m
}

// ShowApproval switches the input area to an approval prompt.
func (m Model) ShowApproval(req interpreter.Approval) Model {
	if m.input.kind != inputApproval {
		m.approval.previousInputKind = m.input.kind
		m.approval.restoreFocus = m.activeFocus
	}
	return m.enterApprovalMode(req)
}

func (m Model) enterApprovalMode(req interpreter.Approval) Model {
	m.input.approval = m.input.approval.Set(req)
	m.input.kind = inputApproval
	m.activeFocus = focusInput
	m.input.compose = m.input.compose.Blur()
	if m.lastSize.Width > 0 && m.lastSize.Height > 0 {
		m = m.HandleResize(m.lastSize.Width, m.lastSize.Height)
	}
	return m
}

// ClearApproval returns to compose input mode and clears the approval prompt.
func (m Model) ClearApproval() (Model, tea.Cmd) {
	m = m.exitApprovalMode()
	if m.activeFocus == focusInput && m.input.kind == inputCompose {
		return m.composeFocus()
	}
	return m, nil
}

func (m Model) exitApprovalMode() Model {
	restore := m.approval.restoreFocus
	previousInputKind := m.approval.previousInputKind
	m.input.approval = m.input.approval.Clear()
	m.input.kind = previousInputKind
	m.activeFocus = restore
	m.approval = approvalState{
		previousInputKind: inputCompose,
		restoreFocus:      focusInput,
	}
	if m.lastSize.Width > 0 && m.lastSize.Height > 0 {
		m = m.HandleResize(m.lastSize.Width, m.lastSize.Height)
	}
	return m
}

func (m Model) composeFocus() (Model, tea.Cmd) {
	var cmd tea.Cmd
	m.input.compose, cmd = m.input.compose.Focus()
	return m, cmd
}

// SetDebug enables or disables debug features on the room.
func (m Model) SetDebug(enabled bool) Model {
	m.debug = enabled
	return m
}

// ToggleDebugRowNums toggles the history row-number overlay.
func (m Model) ToggleDebugRowNums() Model {
	m.history = m.history.ToggleDebugRowNums()
	return m
}

// HandleResize sets the component sizes.
//
// totalH is the height available to the room (it excludes any outer chrome like
// the toolbox and bottom padding).
func (m Model) HandleResize(innerW, totalH int) Model {
	wasAtBottom := false
	if m.history.Ready() {
		wasAtBottom = m.history.ScrollStats().AtBottom
	}
	m.lastSize = tea.WindowSizeMsg{Width: innerW, Height: totalH}
	m.input.compose = m.input.compose.SetWidth(innerW).SetMaxHeightFromTotal(totalH)
	// Layout:
	//   header (1 line)
	//   history (variable height)
	//   top separator (1 line)
	//   input (either composer or approval view; composer height is dynamic)
	//   bottom separator (1 line)
	inputH := m.input.height(totalH)
	if m.input.kind == inputStaged && m.input.staged.status != "" {
		inputH++
	}
	h := max(totalH-(3+inputH), 1)
	m.history = m.history.SetSize(innerW, h)
	m.history = m.history.RebuildColors()
	m = m.syncHistoryAnchorAfterResize(wasAtBottom)
	return m
}

// AppendUserInput appends a user input record to history.
func (m Model) AppendUserInput(body string, routing []string) Model {
	return m.appendPresentationRecord(roomstate.Record{Kind: roomstate.KindUserInput, Text: body, Routing: routing})
}

// AppendSystem appends a system record to history.
func (m Model) AppendSystem(text string) Model {
	return m.appendPresentationRecord(roomstate.Record{Kind: roomstate.KindSystem, Text: text})
}

// GotoBottom scrolls history to the bottom without changing live/browse mode.
func (m Model) GotoBottom() Model {
	m.history = m.history.GotoBottom()
	return m
}

// GoLive arms follow mode and anchors history at the live tail.
func (m Model) GoLive() Model {
	m.historyLive = true
	return m.syncHistoryFollowAnchor()
}

// DebugLabel returns the separator label suffix used in debug mode.
func (m Model) DebugLabel() string { return m.history.DebugLabel() }

// HistoryDebugSummary returns a multi-line summary of the history viewport.
func (m Model) HistoryDebugSummary() string { return m.history.DebugSummary() }

// AtBottom reports whether the history viewport is at the bottom.
func (m Model) AtBottom() bool { return m.history.AtBottom() }

// YOffset returns the history viewport vertical offset.
func (m Model) YOffset() int { return m.history.YOffset() }

// HistoryCursorPosition returns the history cursor row and column.
func (m Model) HistoryCursorPosition() (int, int) { return m.history.CursorPosition() }

// HistoryHasSelection reports whether the history surface has an active selection.
func (m Model) HistoryHasSelection() bool { return m.history.HasSelection() }

// HistorySelectedText returns the active visible selection as plain text.
func (m Model) HistorySelectedText() (string, bool) { return m.history.SelectedText() }

func (m Model) syncHistoryAnchorAfterResize(wasAtBottom bool) Model {
	if m.historyLive {
		return m.syncHistoryFollowAnchor()
	}
	if wasAtBottom && m.activeFocus != focusHistory {
		return m.historyAtBottomBrowse()
	}
	if m.activeFocus == focusHistory {
		m.history = m.history.RevealCursor()
	}
	return m
}

func (m Model) syncHistoryFollowAnchor() Model {
	if !m.historyLive {
		return m
	}
	if m.activeFocus == focusHistory {
		m.history = m.history.GotoLiveEnd()
		return m
	}
	m.history = m.history.GotoBottom()
	return m
}

func (m Model) historyAtBottomBrowse() Model {
	m.history = m.history.GotoBottom()
	return m
}

// Init returns the composer initialization command.
func (m Model) Init() tea.Cmd { return m.input.compose.Init() }
