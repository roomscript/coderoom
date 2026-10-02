package room

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/trigosec/coderoom/internal/interpreter"
	"github.com/trigosec/coderoom/internal/ui/editor"
	"github.com/trigosec/coderoom/internal/ui/room/approval"
	"github.com/trigosec/coderoom/internal/ui/room/history"
)

// Update handles incoming messages and returns the next model state.
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	if m.input.kind == inputApproval {
		if next, cmd, ok := m.handleApprovalMessage(msg); ok {
			return next, cmd
		}
	}
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		return m.handleKey(msg)
	case tea.MouseWheelMsg:
		return m.handleMouseWheel(msg)
	case tea.WindowSizeMsg:
		// Parent is expected to call HandleResize with a height already adjusted
		// for outer chrome; ignore direct WindowSizeMsg here.
		return m, nil
	case editor.Response:
		return m.handleEditorResult(msg), nil
	default:
		// Non-key messages (cursor blink, mouse events, etc).
		//
		// Compose needs these (e.g. cursor blink), but the approval input does not.
		var inputCmd tea.Cmd
		if m.input.kind == inputCompose {
			m.input.compose, inputCmd = m.input.compose.Update(msg)
			m = m.syncAfterCompose()
		}
		var historyCmd tea.Cmd
		m.history, historyCmd = m.history.Update(msg)
		return m, tea.Batch(inputCmd, historyCmd)
	}
}

func (m Model) handleMouseWheel(msg tea.MouseWheelMsg) (Model, tea.Cmd) {
	var cmd tea.Cmd
	m.history, cmd = m.history.Update(msg)
	return m.syncHistoryAfterMouseWheel(), cmd
}

func (m Model) syncHistoryAfterMouseWheel() Model {
	if m.activeFocus != focusHistory {
		m.historyLive = m.history.AtBottom()
		return m
	}

	m.history = m.history.ClearSelection()
	if m.history.AtBottom() {
		m.historyLive = true
		m.history = m.history.GotoLiveEnd()
		return m
	}

	m.historyLive = false
	m.history = m.history.ShowCursorInViewport()
	return m
}

func (m Model) handleKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	k := msg.Key()
	if isCtrlKey(msg, 'o') {
		return m.toggleFocus()
	}

	// PgUp/PgDn always scroll history, regardless of focus.
	if next, handled := m.handlePagingKey(k); handled {
		return next, nil
	}

	return m.handleFocusedKey(msg)
}

func (m Model) handlePagingKey(k tea.Key) (Model, bool) {
	if m.activeFocus == focusHistory && k.Mod.Contains(tea.ModShift) {
		return m, false
	}
	switch k.Code {
	case tea.KeyPgUp:
		return m.pageUp(), true
	case tea.KeyPgDown:
		return m.pageDown(), true
	default:
		return m, false
	}
}

func (m Model) pageUp() Model {
	if m.activeFocus == focusHistory {
		m.history = m.history.ClearSelection()
		m.history = m.history.CursorPageUp()
		m.historyLive = m.history.CursorAtLiveEnd()
		return m
	}
	m.history = m.history.HalfPageUp()
	m.historyLive = false
	return m
}

func (m Model) pageDown() Model {
	if m.activeFocus == focusHistory {
		m.history = m.history.ClearSelection()
		m.history = m.history.CursorPageDown()
		m.historyLive = m.history.CursorAtLiveEnd()
		return m
	}
	m.history = m.history.HalfPageDown()
	m.historyLive = m.history.AtBottom()
	return m
}

func (m Model) handleFocusedKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	if m.activeFocus == focusHistory {
		return m.handleHistoryKey(msg)
	}
	if m.input.kind == inputApproval {
		return m.handleApprovalKey(msg)
	}
	return m.handleComposeKey(msg)
}

func (m Model) toggleFocus() (Model, tea.Cmd) {
	if m.activeFocus == focusInput {
		m.activeFocus = focusHistory
		m.input.compose = m.input.compose.Blur()
		if m.historyLive || m.history.AtBottom() {
			m.history = m.history.GotoLiveEnd()
			m.historyLive = true
		} else {
			m.history = m.history.AdoptCursorFromViewport()
			m.historyLive = false
		}
		return m, nil
	}
	m.history = m.history.ClearSelection()
	m.activeFocus = focusInput
	if m.input.kind == inputCompose {
		return m.composeFocus()
	}
	return m, nil
}

func (m Model) handleApprovalKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	if isCtrlKey(msg, 'c') {
		// Treat Ctrl+C as cancel when an approval is active.
		next, cmd, _ := m.handleApprovalMessage(approval.CancelMsg{})
		return next, cmd
	}
	var cmd tea.Cmd
	m.input.approval, cmd = m.input.approval.Update(msg)
	// Confirmation/cancel is signaled via messages.
	return m, cmd
}

func (m Model) handleComposeKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	if m.input.kind == inputStaged {
		return m.handleStagedComposeKey(msg)
	}

	return m.handleDraftComposeKey(msg)
}

func (m Model) handleStagedComposeKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	k := msg.Key()
	switch {
	case k.Code == tea.KeyEsc:
		// Return to draft mode with the staged text loaded for editing.
		m = m.ClearComposerStaged()
		m.activeFocus = focusInput
		next, focusCmd := m.composeFocus()
		return next, tea.Batch(focusCmd, func() tea.Msg { return StagedEditMsg{} })
	case isCtrlKey(msg, 'x'):
		return m, func() tea.Msg { return StagedInterruptMsg{} }
	case isCtrlKey(msg, 'c'):
		if m.input.compose.Value() == "" {
			return m, nil
		}
		m.input.compose = m.input.compose.Reset()
		m = m.ClearComposerStaged()
		m = m.syncAfterCompose()
		return m, func() tea.Msg { return StagedClearMsg{} }
	default:
		// Ignore all other keys while staged (read-only).
		return m, nil
	}
}

func (m Model) handleDraftComposeKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	k := msg.Key()
	switch {
	case isCtrlKey(msg, 'g'):
		return m.startEditorCompose()
	case isCtrlKey(msg, 'v'):
		return m.pasteComposeClipboard()
	case k.Code == tea.KeyEnter && !k.Mod.Contains(tea.ModAlt):
		raw := m.input.compose.Value()
		if strings.TrimSpace(raw) == "" {
			return m, nil
		}
		m.input.compose = m.input.compose.Reset()
		m = m.syncAfterCompose()
		return m, func() tea.Msg { return SubmitMsg{Text: raw} }
	default:
		var cmd tea.Cmd
		m.input.compose, cmd = m.input.compose.Update(msg)
		m = m.syncAfterCompose()
		return m, cmd
	}
}

func (m Model) pasteComposeClipboard() (Model, tea.Cmd) {
	if m.clipboardRead == nil {
		return m, nil
	}
	text, err := m.clipboardRead()
	if err != nil {
		return m.AppendSystem("error: paste failed: " + err.Error()), nil
	}
	var cmd tea.Cmd
	m.input.compose, cmd = m.input.compose.Update(tea.PasteMsg{Content: text})
	m = m.syncAfterCompose()
	return m, cmd
}

func (m Model) handleHistoryKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	k := msg.Key()
	if nextHistory, handled := applyHistoryModifiedSelectionKey(m.history, k); handled {
		m.history = nextHistory
		m.historyLive = m.history.CursorAtLiveEnd()
		return m, nil
	}
	if k.Mod.Contains(tea.ModCtrl) {
		return m.handleHistoryCtrlKey(msg)
	}
	if k.Code == tea.KeyEsc {
		if m.history.HasSelection() {
			m.history = m.history.ClearSelection()
			m.historyLive = m.history.CursorAtLiveEnd()
			return m, nil
		}
		m.activeFocus = focusInput
		if m.input.kind == inputCompose {
			return m.composeFocus()
		}
		return m, nil
	}
	if k.Mod.Contains(tea.ModShift) {
		if nextHistory, handled := applyHistorySelectionKey(m.history, k); handled {
			m.history = nextHistory
			m.historyLive = m.history.CursorAtLiveEnd()
			return m, nil
		}
	}
	if nextHistory, handled := applyHistoryCursorKey(m.history, k); handled {
		m.history = nextHistory.ClearSelection()
		m.historyLive = m.history.CursorAtLiveEnd()
		return m, nil
	}
	var cmd tea.Cmd
	m.history, cmd = m.history.Update(msg)
	return m, cmd
}

func applyHistoryModifiedSelectionKey(historyModel history.Model, key tea.Key) (history.Model, bool) {
	if !key.Mod.Contains(tea.ModCtrl) || !key.Mod.Contains(tea.ModShift) {
		return historyModel, false
	}
	return applyHistoryWordSelectionKey(historyModel, key)
}

func applyHistoryWordSelectionKey(historyModel history.Model, key tea.Key) (history.Model, bool) {
	switch key.Code {
	case tea.KeyLeft:
		return historyModel.SelectWordLeft(), true
	case tea.KeyRight:
		return historyModel.SelectWordRight(), true
	default:
		return historyModel, false
	}
}

func (m Model) handleHistoryCtrlKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	switch {
	case isCtrlKey(msg, 'c'):
		return m.copyHistorySelection(), nil
	case isCtrlKey(msg, 'g'):
		return m.openEditorWithTranscript()
	default:
		var cmd tea.Cmd
		m.history, cmd = m.history.Update(msg)
		return m, cmd
	}
}

func isCtrlKey(msg tea.KeyPressMsg, key rune) bool {
	if msg.String() == "ctrl+"+string(key) {
		return true
	}
	k := msg.Key()
	return k.Code == key && k.Mod.Contains(tea.ModCtrl)
}

func (m Model) copyHistorySelection() Model {
	selected, ok := m.history.SelectedText()
	if !ok || m.clipboardWrite == nil {
		return m
	}
	if err := m.clipboardWrite(selected); err != nil {
		return m.AppendSystem("error: copy failed: " + err.Error())
	}
	return m
}

func applyHistoryCursorKey(historyModel history.Model, key tea.Key) (history.Model, bool) {
	switch key.Code {
	case tea.KeyUp:
		return historyModel.CursorUp(), true
	case tea.KeyDown:
		return historyModel.CursorDown(), true
	case tea.KeyLeft:
		return historyModel.CursorLeft(), true
	case tea.KeyRight:
		return historyModel.CursorRight(), true
	case tea.KeyHome:
		return historyModel.CursorLineStart(), true
	case tea.KeyEnd:
		return historyModel.CursorLineEnd(), true
	default:
		return historyModel, false
	}
}

func applyHistorySelectionKey(historyModel history.Model, key tea.Key) (history.Model, bool) {
	switch key.Code {
	case tea.KeyUp:
		return historyModel.SelectUp(), true
	case tea.KeyDown:
		return historyModel.SelectDown(), true
	case tea.KeyLeft:
		return historyModel.SelectLeft(), true
	case tea.KeyRight:
		return historyModel.SelectRight(), true
	case tea.KeyHome:
		return historyModel.SelectLineStart(), true
	case tea.KeyEnd:
		return historyModel.SelectLineEnd(), true
	case tea.KeyPgUp:
		return historyModel.SelectPageUp(), true
	case tea.KeyPgDown:
		return historyModel.SelectPageDown(), true
	default:
		return historyModel, false
	}
}

func (m Model) startEditorCompose() (Model, tea.Cmd) {
	prior := m.input.compose.Value()
	cmd, err := editor.OpenTempFileInEditor(editor.Request{
		Purpose:          editor.PurposeCompose,
		PriorText:        prior,
		InitialText:      prior,
		TempPattern:      "coderoom-compose-*.md",
		TrimFinalNewline: true,
	})
	if err != nil {
		m = m.AppendSystem("error: " + err.Error())
		return m, nil
	}
	return m, cmd
}

func (m Model) handleEditorResult(msg editor.Response) Model {
	switch msg.Purpose {
	case editor.PurposeCompose:
		if msg.Canceled || msg.Err != nil {
			m.input.compose = m.input.compose.SetValue(msg.PriorText)
		} else {
			m.input.compose = m.input.compose.SetValue(msg.NewText)
		}
		return m.syncAfterCompose()
	case editor.PurposeTranscript:
		return m
	default:
		return m
	}
}

func (m Model) handleApprovalMessage(msg tea.Msg) (Model, tea.Cmd, bool) {
	switch msg.(type) {
	case approval.ConfirmMsg:
		opt, ok := m.input.approval.SelectedOption()
		if !ok {
			next, focusCmd := m.ClearApproval()
			return next, focusCmd, true
		}
		next, focusCmd := m.ClearApproval()
		decisionCmd := func() tea.Msg { return ApprovalDecisionMsg{Choice: opt} }
		return next, tea.Batch(focusCmd, decisionCmd), true
	case approval.CancelMsg:
		choice := interpreter.ApprovalChoice{OptionID: "decline"}
		if approvalHasOption(m.input.approval.Options(), "cancel") {
			choice = interpreter.ApprovalChoice{OptionID: "cancel"}
		}
		next, focusCmd := m.ClearApproval()
		decisionCmd := func() tea.Msg { return ApprovalDecisionMsg{Choice: choice} }
		return next, tea.Batch(focusCmd, decisionCmd), true
	default:
		return m, nil, false
	}
}

func approvalHasOption(opts []interpreter.ApprovalOption, want string) bool {
	for _, opt := range opts {
		if opt.ID == want {
			return true
		}
	}
	return false
}

func (m Model) openEditorWithTranscript() (Model, tea.Cmd) {
	content := ansi.Strip(m.history.PlainText())
	cmd, err := editor.OpenTempFileInEditor(editor.Request{
		Purpose:     editor.PurposeTranscript,
		InitialText: content,
		TempPattern: "coderoom-transcript-*.txt",
		ReadOnly:    true,
	})
	if err != nil {
		m = m.AppendSystem("error: " + err.Error())
		return m, nil
	}
	return m, cmd
}

// syncAfterCompose adjusts history height to match the current compose height,
// preserving the bottom anchor if history was already at bottom.
func (m Model) syncAfterCompose() Model {
	if !m.history.Ready() {
		return m
	}
	totalH := m.lastSize.Height
	if totalH <= 0 {
		return m
	}
	inputH := m.input.compose.Height()
	if m.input.kind == inputStaged && m.input.staged.status != "" {
		inputH++
	}
	newHistH := max(totalH-(3+inputH), 1)
	if newHistH == m.history.Height() {
		return m
	}
	wasAtBottom := m.history.AtBottom()
	m.history = m.history.SetHeight(newHistH)
	return m.syncHistoryAnchorAfterResize(wasAtBottom)
}
