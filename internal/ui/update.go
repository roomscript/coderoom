package ui

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/roomscript/coderoom/internal/interpreter"
	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/promptlang"
	"github.com/roomscript/coderoom/internal/ui/room"
)

const (
	// marginH is the number of columns reserved on each horizontal side. Only a
	// left prefix is applied in View(); the right margin is implicit because
	// viewport, separator, and input are all sized to inner = width-2*marginH.
	marginH = 2
	// marginV is the number of empty rows below the input.
	marginV = 1
)

// Init starts the interpreter event listener; called once by Bubble Tea on startup.
func (m Model) Init() tea.Cmd {
	return tea.Batch(awaitInterpreterEvent(m.interpreterQueue), m.room.Init())
}

// Update handles incoming messages and returns the next model state.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		return m.handleKey(msg)
	case tea.WindowSizeMsg:
		return m.handleResize(msg), nil
	case interpreterEventMsg:
		next, cmd := m.handleInterpreterEvent(msg.event)
		return next, tea.Batch(cmd, awaitInterpreterEvent(m.interpreterQueue))
	default:
		return m.handleNonSessionMessage(msg)
	}
}

func (m Model) handleNonSessionMessage(msg tea.Msg) (tea.Model, tea.Cmd) {
	if next, cmd, handled := m.handleStageOperationMessage(msg); handled {
		return next, cmd
	}
	switch msg := msg.(type) {
	case stageTakenForEditMsg:
		return m.handleStageTakenForEdit(msg), nil
	case stageDiscardedMsg, stageInterruptRequestedMsg:
		return m, nil
	case room.SubmitMsg:
		return m.submit(msg.Text)
	case room.ApprovalDecisionMsg:
		return m.handleApprovalDecision(msg)
	default:
		return m.forwardMessage(msg)
	}
}

func (m Model) handleStageOperationMessage(msg tea.Msg) (tea.Model, tea.Cmd, bool) {
	switch msg.(type) {
	case room.StagedEditMsg:
		if m.interpreterStagePresented {
			return m, takeStageForEdit(m.interpreter), true
		}
		m.room = m.room.ClearComposerStaged()
		return m, nil, true
	case room.StagedClearMsg:
		if m.interpreterStagePresented {
			return m, discardStage(m.interpreter), true
		}
		m.room = m.room.ClearComposerStaged()
		return m, nil, true
	case room.StagedInterruptMsg:
		if m.interpreterStagePresented {
			return m, interruptAndDispatchStage(m.interpreter), true
		}
		return m, nil, true
	default:
		return m, nil, false
	}
}

func (m Model) handleStageTakenForEdit(msg stageTakenForEditMsg) Model {
	if !msg.ok {
		return m
	}
	// A successful take may race with already queued snapshots. If the clear
	// snapshot has not arrived yet, ignore older non-nil snapshots until it does.
	m.preserveStageDraft = m.interpreterStagePresented
	m.room = m.room.ClearComposerStaged()
	if m.room.ComposeValue() == "" {
		m.room = m.room.SetComposeValue(msg.raw)
	}
	return m
}

func (m Model) submit(raw string) (Model, tea.Cmd) {
	if strings.TrimSpace(raw) == "" {
		return m, nil
	}
	statement, err := promptlang.Parse(raw)
	if err != nil || !isUIOnlyStatement(statement) {
		return m.submitToInterpreter(raw), nil
	}
	m.releaseSubmissionGate()
	return m.handleSubmit(raw)
}

func isUIOnlyStatement(statement promptlang.Statement) bool {
	switch statement.(type) {
	case promptlang.DebugView, promptlang.DebugRows:
		return true
	default:
		return false
	}
}

func (m Model) submitToInterpreter(raw string) Model {
	if strings.TrimSpace(raw) == "" {
		return m
	}
	if m.submissionPending {
		if m.submissionAwaitingDispatch != raw {
			return m.restoreSubmittedComposer(raw)
		}
		m.submissionAwaitingDispatch = ""
	}
	if err := m.interpreter.Submit(raw); err != nil {
		m.submissionPending = false
		return m.restoreSubmittedComposer(raw)
	}
	m.submissionPending = true
	m.room = m.clearSubmittedComposer(raw)
	return m
}

func (m Model) handleInterpreterEvent(event interpreter.Event) (Model, tea.Cmd) {
	if next, cmd, handled := m.handleInterpreterPresentationEvent(event); handled {
		return next, cmd
	}
	switch event := event.(type) {
	case interpreter.UnknownCommand:
		m.releaseSubmissionGate()
		if event.Name != "" {
			err := promptlang.UndefinedCommandError{Name: event.Name}
			m.room = m.room.AppendSystem(fmt.Sprintf("error: invoke /%s: %v", event.Name, err))
			return m, nil
		}
		return m.handleSubmit(event.Raw)
	case interpreter.InputRejected:
		m.releaseSubmissionGate()
		m.room = m.room.AppendSystem(formatInputRejection(event.Err))
		return m, nil
	case interpreter.SubmissionSucceeded:
		m.releaseSubmissionGate()
		m.stagedDispatchRaw = ""
		m = m.renderSubmissionSuccess(event.Raw)
		return m, nil
	case interpreter.SubmissionFailed:
		m.releaseSubmissionGate()
		m = m.restoreFailedStagedDraft(event.Raw)
		m.stagedDispatchRaw = ""
		m.room = m.room.AppendSystem(formatSubmissionFailure(event))
		return m, nil
	default:
		return m, nil
	}
}

func (m Model) restoreFailedStagedDraft(raw string) Model {
	statement, err := promptlang.Parse(raw)
	if err != nil {
		return m
	}
	switch statement.(type) {
	case promptlang.Send, promptlang.Broadcast, promptlang.Handoff:
	default:
		return m
	}
	if m.stagedDispatchRaw == raw {
		return m
	}
	return m.restoreSubmittedComposer(raw)
}

func formatSubmissionFailure(event interpreter.SubmissionFailed) string {
	if event.Code == interpreter.ErrorParticipantUnavailable {
		return "error: " + event.Err.Error()
	}
	statement, err := promptlang.Parse(event.Raw)
	if err == nil {
		switch action := statement.(type) {
		case promptlang.Invite:
			return fmt.Sprintf("error: invite %q: %v", action.Alias, event.Err)
		case promptlang.Remove:
			return fmt.Sprintf("error: remove %q: %v", action.Alias, event.Err)
		case promptlang.Cancel:
			return fmt.Sprintf("error: cancel %q: %v", action.Alias, event.Err)
		case promptlang.PolicyEnable:
			return "error: policy: " + event.Err.Error()
		}
	}
	return fmt.Sprintf("error: %s: %v", event.Operation, event.Err)
}

func (m Model) renderSubmissionSuccess(raw string) Model {
	statement, err := promptlang.Parse(raw)
	if err != nil {
		return m
	}
	switch action := statement.(type) {
	case promptlang.Cancel:
		m.room = m.room.AppendSystem("[→ " + action.Alias + "] cancel requested")
	case promptlang.PolicyEnable:
		m.room = m.room.AppendSystem("[policy] " + string(action.Name) + " enabled")
	}
	return m
}

func (m Model) handleInterpreterPresentationEvent(event interpreter.Event) (Model, tea.Cmd, bool) {
	if next, handled := m.handleInterpreterTranscriptEvent(event); handled {
		return next, nil, true
	}
	switch event := event.(type) {
	case interpreter.StateChanged:
		next, cmd := m.presentInterpreterSnapshot(event.Snapshot)
		return next, cmd, true
	case interpreter.RosterListed:
		return m.renderRoster(event.Participants), nil, true
	case interpreter.HelpListed:
		return m.renderHelp(event), nil, true
	case interpreter.ExitRequested:
		return m, tea.Quit, true
	case interpreter.ShellCompleted:
		return m, nil, true
	case interpreter.LoopStatus:
		return m, nil, true
	case interpreter.OperationFailed:
		m.room = m.room.AppendSystem(fmt.Sprintf("error: %s: %v", event.Operation, event.Err))
		return m, nil, true
	default:
		return m, nil, false
	}
}

func (m Model) handleInterpreterTranscriptEvent(event interpreter.Event) (Model, bool) {
	switch event := event.(type) {
	case interpreter.TranscriptChanged:
		m.room = m.room.ApplyTranscript(event.Delta)
		return m, true
	case interpreter.InputAccepted:
		m.stagedDispatchRaw = ""
		return m, true
	case interpreter.StagedInputDispatched:
		m.stagedDispatchRaw = event.Raw
		return m, true
	case interpreter.HandoffCompleted:
		return m, true
	case interpreter.StagedInputDiscarded:
		m.stagedDispatchRaw = ""
		m.room = m.room.AppendSystem(event.Reason)
		m = m.restoreSubmittedComposer(event.Raw)
		m.preserveStageDraft = true
		return m, true
	default:
		return m, false
	}
}

func (m Model) renderRoster(participants []participant.View) Model {
	if len(participants) == 0 {
		m.room = m.room.AppendSystem("[no agents]")
		return m
	}
	aliases := make([]string, len(participants))
	for index, view := range participants {
		aliases[index] = view.Alias
	}
	slices.Sort(aliases)
	m.room = m.room.AppendSystem("[agents] " + strings.Join(aliases, ", "))
	return m
}

func formatInputRejection(err error) string {
	var unknown promptlang.UnknownCommandError
	if errors.As(err, &unknown) {
		return "error: " + err.Error() + " (type /help)"
	}
	return "error: " + err.Error()
}

func (m Model) handleApprovalDecision(msg room.ApprovalDecisionMsg) (tea.Model, tea.Cmd) {
	choice := msg.Choice
	if err := m.interpreter.ResolveApproval(m.activeApprovalID, choice); err != nil {
		m.room = m.room.AppendSystem(fmt.Sprintf("error: resolve approval: %v", err))
		return m, nil
	}
	m.activeApprovalID = 0
	return m, nil
}

func (m Model) forwardMessage(msg tea.Msg) (tea.Model, tea.Cmd) {
	var roomCmd tea.Cmd
	m.room, roomCmd = m.room.Update(msg)
	var toolboxCmd tea.Cmd
	m.toolbox, toolboxCmd = m.toolbox.Update(msg)
	return m, tea.Batch(roomCmd, toolboxCmd)
}

func (m Model) handleKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	raw := m.room.ComposeValue()
	var cmd tea.Cmd
	m.room, cmd = m.room.Update(msg)
	if !isSubmittedComposer(msg, raw, m.room.ComposeValue(), cmd) {
		return m, cmd
	}
	if m.submissionPending {
		m.room = m.room.SetComposeValue(raw)
		return m, nil
	}
	m.submissionPending = true
	m.submissionAwaitingDispatch = raw
	return m, cmd
}

func isSubmittedComposer(msg tea.KeyPressMsg, before, after string, cmd tea.Cmd) bool {
	key := msg.Key()
	return key.Code == tea.KeyEnter &&
		!key.Mod.Contains(tea.ModAlt) &&
		strings.TrimSpace(before) != "" &&
		after == "" &&
		cmd != nil
}

func (m Model) handleResize(msg tea.WindowSizeMsg) Model {
	m.lastSize = msg
	inner := max(msg.Width-2*marginH, 1)
	m.toolbox = m.toolbox.SetWidth(inner)
	roomH := max(msg.Height-(m.toolbox.Height()+marginV), 1)
	m.room = m.room.HandleResize(inner, roomH)
	m.room = m.room.SetDebug(m.debug)
	if m.showStartupHelpTip && m.room.Ready() && len(m.room.HistoryRecords()) == 0 {
		m.room = m.room.AppendSystem("tip: type /help for commands and shortcuts")
		m.showStartupHelpTip = false
	}
	return m
}

func (m Model) handleSubmit(raw string) (Model, tea.Cmd) {
	if strings.TrimSpace(raw) == "" {
		return m, nil
	}
	action, err := promptlang.Parse(raw)
	if err != nil {
		var unknown promptlang.UnknownCommandError
		if errors.As(err, &unknown) {
			m.room = m.room.AppendSystem("error: " + err.Error() + " (type /help)")
			m.room = m.clearSubmittedComposer(raw)
			return m, nil
		}
		m.room = m.room.AppendSystem("error: " + err.Error())
		m.room = m.clearSubmittedComposer(raw)
		return m, nil
	}

	m.room = m.room.AppendUserInput(raw, nil)
	m.room = m.clearSubmittedComposer(raw)
	m, _ = m.executeDebugAction(action)
	return m, nil
}

func (m Model) clearSubmittedComposer(raw string) room.Model {
	if m.room.ComposeValue() != raw {
		return m.room
	}
	return m.room.SetComposeValue("")
}

func (m Model) restoreSubmittedComposer(raw string) Model {
	if m.room.ComposeValue() == "" {
		m.room = m.room.SetComposeValue(raw)
	}
	return m
}

func (m *Model) releaseSubmissionGate() {
	m.submissionPending = false
	m.submissionAwaitingDispatch = ""
}

func (m Model) presentInterpreterSnapshot(snapshot interpreter.Snapshot) (Model, tea.Cmd) {
	m = m.updateParticipantColors(snapshot.Participants)
	var cmd tea.Cmd
	m.toolbox, cmd = m.toolbox.SetParticipants(snapshot.Participants)
	m = m.presentInterpreterStage(snapshot)
	if snapshot.Approval == nil {
		if m.activeApprovalID != 0 {
			m.activeApprovalID = 0
			m.room, _ = m.room.ClearApproval()
		}
		return m, cmd
	}
	if m.activeApprovalID == snapshot.Approval.ID {
		return m, cmd
	}
	approval := snapshot.Approval
	m.activeApprovalID = approval.ID
	req := *approval
	if strings.TrimSpace(approval.Alias) != "" {
		req.Prompt = "[→ " + approval.Alias + "] " + req.Prompt
	}
	m.room = m.room.ShowApproval(req)
	return m, cmd
}

func (m Model) updateParticipantColors(participants []participant.View) Model {
	colors := make(map[string]string, len(participants))
	for _, view := range participants {
		colors[view.Alias] = view.Color
	}
	if maps.Equal(m.colors, colors) {
		return m
	}
	// Keep the map captured by the history color resolver alive.
	clear(m.colors)
	maps.Copy(m.colors, colors)
	m.room = m.room.RefreshColors()
	return m
}

func (m Model) executeDebugAction(a promptlang.Statement) (Model, bool) {
	switch a.(type) {
	case promptlang.DebugView:
		if !m.debug {
			m.room = m.room.AppendSystem("error: debug commands disabled (set CODEROOM_DEBUG=1)")
			return m, true
		}
		return m.debugView(), true
	case promptlang.DebugRows:
		if !m.debug {
			m.room = m.room.AppendSystem("error: debug commands disabled (set CODEROOM_DEBUG=1)")
			return m, true
		}
		m.room = m.room.ToggleDebugRowNums()
		return m, true
	default:
		return m, false
	}
}

const helpKeysText = `General keys:
  Ctrl+O               toggle focus (compose ⇄ history)
  PgUp / PgDn          scroll transcript (works in any focus)

Compose focus (separator label: compose):
  Enter                submit
  Ctrl+C               clear composer
  Ctrl+V               paste system clipboard
  Ctrl+G               open $EDITOR for multi-line compose
  Ctrl+X               (when staged) interrupt + send
  Esc                  (when staged) edit staged message

History focus (separator label: history):
  ↑ / ↓                scroll 1 line
  Home / End           jump to top / jump to bottom
  Esc                  return to compose focus
  Ctrl+G               open transcript in $EDITOR (read-only)
  Ctrl+C               copy to system clipboard

Approval prompt (separator label: approval):
  ↑/↓ or j/k           change selection
  Enter                confirm selection
  Esc                  dismiss prompt
  Ctrl+C               cancel prompt

UI hints:
  The separator label shows the current focus: compose/history/approval
  When history is focused, the first visible history row is highlighted`

func (m Model) renderHelp(help interpreter.HelpListed) Model {
	var text strings.Builder
	text.WriteString("[help]\n\nCommands:\n")
	writeHelpEntries(&text, help.Commands)
	writeHelpEntries(&text, m.debugHelpEntries())
	text.WriteString("\nSending messages:\n")
	writeHelpEntries(&text, help.Messages)
	text.WriteString("\n")
	text.WriteString(helpKeysText)
	m.room = m.room.AppendSystem(text.String())
	return m
}

func writeHelpEntries(text *strings.Builder, entries []interpreter.HelpEntry) {
	for _, entry := range entries {
		if len(entry.Usage) > 20 {
			fmt.Fprintf(text, "  %s\n  %-20s %s\n", entry.Usage, "", entry.Description)
			continue
		}
		fmt.Fprintf(text, "  %-20s %s\n", entry.Usage, entry.Description)
	}
}

func (m Model) debugHelpEntries() []interpreter.HelpEntry {
	if !m.debug {
		return nil
	}
	return []interpreter.HelpEntry{
		{Usage: "/debugview", Description: "print viewport debug"},
		{Usage: "/debugrows", Description: "toggle row number overlay"},
	}
}

func (m Model) debugView() Model {
	if !m.room.Ready() {
		m.room = m.room.AppendSystem("[debug] not ready")
		return m
	}
	m.room = m.room.AppendSystem("[debug]\n" + m.room.HistoryDebugSummary())
	return m
}
