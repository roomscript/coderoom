package ui

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/trigosec/coderoom/internal/interpreter"
	"github.com/trigosec/coderoom/internal/participant"
	"github.com/trigosec/coderoom/internal/promptlang"
	roomstate "github.com/trigosec/coderoom/internal/room"
	"github.com/trigosec/coderoom/internal/session"
	"github.com/trigosec/coderoom/internal/ui/room"
	"github.com/trigosec/coderoom/internal/ui/room/staging"
)

const (
	// marginH is the number of columns reserved on each horizontal side. Only a
	// left prefix is applied in View(); the right margin is implicit because
	// viewport, separator, and input are all sized to inner = width-2*marginH.
	marginH = 2
	// marginV is the number of empty rows below the input.
	marginV = 1
)

// Init starts the session event listener; called once by Bubble Tea on startup.
func (m Model) Init() tea.Cmd {
	return tea.Batch(awaitEvent(m.queue), awaitInterpreterEvent(m.interpreterQueue), m.room.Init())
}

// Update handles incoming messages and returns the next model state.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		return m.handleKey(msg)
	case tea.WindowSizeMsg:
		return m.handleResize(msg), nil
	case sessionEventMsg:
		next, cmd := m.handleEvent(msg.event)
		return next, tea.Batch(cmd, awaitEvent(m.queue))
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
	case room.UpdateMsg:
		return m.handleRoomUpdate(msg)
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
		next := m.handleStagedInterrupt()
		return next, nil, true
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

func (m Model) handleRoomUpdate(msg room.UpdateMsg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	m.room, cmd = m.room.Update(msg)
	m = m.maybeAdvanceProjectedHandoff(msg.Trigger())
	return m, cmd
}

func (m Model) submit(raw string) (Model, tea.Cmd) {
	if strings.TrimSpace(raw) == "" {
		return m, nil
	}
	statement, err := promptlang.Parse(raw)
	if err != nil || isNativeInterpreterStatement(statement) {
		return m.submitToInterpreter(raw), nil
	}
	m.releaseSubmissionGate()
	return m.handleSubmit(raw)
}

func isNativeInterpreterStatement(statement promptlang.Statement) bool {
	switch statement.(type) {
	case promptlang.Invite, promptlang.Remove, promptlang.Cancel, promptlang.PolicyEnable,
		promptlang.Shell, promptlang.CommandDefinition, promptlang.CommandInvocation,
		promptlang.Loop, promptlang.Who, promptlang.Help, promptlang.Quit, promptlang.Send:
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
		m = m.renderSubmissionSuccess(event.Raw)
		return m, nil
	case interpreter.SubmissionFailed:
		m.releaseSubmissionGate()
		m.room = m.room.AppendSystem(formatSubmissionFailure(event))
		return m, nil
	default:
		return m, nil
	}
}

func formatSubmissionFailure(event interpreter.SubmissionFailed) string {
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
	case promptlang.CommandDefinition:
		m.room = m.room.AppendSystem("[defined] /" + action.Name)
	}
	return m
}

func (m Model) handleInterpreterPresentationEvent(event interpreter.Event) (Model, tea.Cmd, bool) {
	if next, handled := m.handleInterpreterTranscriptEvent(event); handled {
		return next, nil, true
	}
	switch event := event.(type) {
	case interpreter.StateChanged:
		return m.presentInterpreterStage(event.Snapshot), nil, true
	case interpreter.RosterListed:
		return m.renderRoster(event.Participants), nil, true
	case interpreter.HelpListed:
		return m.renderHelp(event), nil, true
	case interpreter.ExitRequested:
		return m, tea.Quit, true
	case interpreter.ShellCompleted:
		return m.appendShellResult(event), nil, true
	case interpreter.LoopStatus:
		m.room = m.room.AppendSystem(event.Message)
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
	case interpreter.InputAccepted:
		statement, err := promptlang.Parse(event.Raw)
		if err != nil {
			return m, true
		}
		if _, stagedSend := statement.(promptlang.Send); !stagedSend {
			m.room = m.room.AppendUserInput(event.Raw, event.Routing)
		}
		return m, true
	case interpreter.StagedInputDispatched:
		m.room = m.room.AppendUserInput(event.Raw, event.Routing)
		return m, true
	case interpreter.StagedInputDiscarded:
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
	choice := interpreter.ApprovalChoice{OptionID: string(msg.Choice)}
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
	if m.room.HasStagedBatch() {
		// This should be prevented by the room, but keep it defensive.
		m.room = m.room.AppendSystem("error: message already staged (Esc to edit, Ctrl+X to send)")
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

	// Barrier-batch applies to user-authored Send/Broadcast/Handoff only.
	switch action.(type) {
	case promptlang.Send, promptlang.Broadcast, promptlang.Handoff:
		return m.handleBarrierBatchSubmit(raw, action), nil
	default:
	}

	routing := m.routingFor(action)
	m.room = m.room.AppendUserInput(raw, routing)
	m.room = m.clearSubmittedComposer(raw)
	return m.executeAction(action)
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

// routingFor returns the aliases that will receive the action, used to
// populate the routing footer on the echoed user-input record.
func (m Model) routingFor(a promptlang.Statement) []string {
	ps := m.sess.RoutableParticipants()
	var sharedSendRecipients []string
	if send, ok := a.(promptlang.Send); ok {
		sharedSendRecipients = m.sess.PlanSharedSend(send.Alias).Targets()
	}
	return routingFor(a, ps, sharedSendRecipients)
}

func routingFor(a promptlang.Statement, ps []participant.Participant, sharedSendRecipients []string) []string {
	if _, ok := a.(promptlang.Broadcast); ok {
		aliases := make([]string, len(ps))
		for i, p := range ps {
			aliases[i] = p.Alias
		}
		slices.Sort(aliases)
		return aliases
	}
	if _, ok := a.(promptlang.Send); ok {
		return sharedSendRecipients
	}
	if h, ok := a.(promptlang.Handoff); ok {
		if h.FromAlias == h.ToAlias {
			return []string{h.FromAlias}
		}
		return []string{h.FromAlias, h.ToAlias}
	}
	return nil
}

func missingHandoffTarget(act staging.Action, targets []string) string {
	if !slices.Contains(targets, act.FromAlias) {
		return act.FromAlias
	}
	if !slices.Contains(targets, act.ToAlias) {
		return act.ToAlias
	}
	return ""
}

func (m Model) discardStagedBatch(message string) Model {
	m.room = m.room.ClearComposerStaged()
	m.room = m.room.AppendSystem(message)
	return m
}

func (m Model) executeStagedDispatch(act staging.Action, targets []string) (Model, []string, error) {
	switch act.Kind {
	case staging.ActionBroadcast:
		return m.executeBroadcastAll(act.Text)
	case staging.ActionSend:
		return m.executeStagedSend(act, targets)
	case staging.ActionHandoff:
		return m.executeStagedHandoff(act, targets)
	default:
		m.room = m.room.AppendSystem("error: internal: staged action invalid")
		return m, nil, errInvalidStagedAction
	}
}

func (m Model) executeStagedSend(act staging.Action, targets []string) (Model, []string, error) {
	if !slices.Contains(targets, act.Alias) {
		message := fmt.Sprintf("staged message discarded: %q is no longer available", act.Alias)
		return m.discardStagedBatch(message), nil, nil
	}
	return m.executePlannedSendToAgent(act.SendPlan, act.Text)
}

func (m Model) executeStagedHandoff(act staging.Action, targets []string) (Model, []string, error) {
	if missing := missingHandoffTarget(act, targets); missing != "" {
		message := fmt.Sprintf("staged message discarded: %q is no longer available", missing)
		return m.discardStagedBatch(message), nil, nil
	}
	return m.executeHandoff(act.FromAlias, act.ToAlias, targets)
}

var errInvalidStagedAction = errors.New("invalid staged action")

func (m Model) dispatchRoomStagedBatch() Model {
	act, targets, ok := m.room.StagedDispatchCandidate()
	if !ok {
		m.room = m.room.AppendSystem("error: internal: no staged batch to dispatch")
		return m
	}
	if len(targets) == 0 {
		return m.discardStagedBatch("staged message discarded: no active targets")
	}

	m, delivered, err := m.executeStagedDispatch(act, targets)
	if errors.Is(err, errInvalidStagedAction) {
		return m
	}
	if err != nil {
		if len(delivered) > 0 {
			m.room = m.room.CommitStagedBatchDispatch(delivered)
		} else {
			m.room = m.room.ClearComposerStaged()
		}
		return m
	}
	m.room = m.room.CommitStagedBatchDispatch(targets)
	return m
}

func (m Model) handleEvent(e session.Event) (Model, tea.Cmd) {
	next := m.handleMessageEvent(e)
	// Best-effort only — see DrainObserverUpdates' doc comment.
	var triggers []roomstate.UpdateTrigger
	next.room, triggers = next.room.DrainObserverUpdateTriggers()
	for _, trigger := range triggers {
		next = next.maybeAdvanceProjectedHandoff(trigger)
	}
	next = next.maybeAdvanceStagedBatch(e)
	var toolboxCmd tea.Cmd
	next.toolbox, toolboxCmd = next.toolbox.SetParticipants(next.sess.Roster())
	return next, toolboxCmd
}

func (m Model) handleMessageEvent(e session.Event) Model {
	switch e := e.(type) {
	case session.ApprovalRequested:
		m = m.handleApprovalRequested(e)
	case session.ApprovalCleared:
		m = m.handleApprovalCleared(e)
	default:
	}
	return m
}

func (m Model) handleApprovalRequested(e session.ApprovalRequested) Model {
	m.activeApprovalID = e.ID
	req := e.Req
	if strings.TrimSpace(e.Alias) != "" {
		req.Ask = "[→ " + e.Alias + "] " + req.Ask
	}
	m.room = m.room.ShowApproval(req)
	return m
}

func (m Model) handleApprovalCleared(e session.ApprovalCleared) Model {
	if e.ID == 0 || e.ID != m.activeApprovalID {
		return m
	}
	m.activeApprovalID = 0
	m.room, _ = m.room.ClearApproval()
	return m
}

func (m Model) handleBarrierBatchSubmit(raw string, action promptlang.Statement) Model {
	ps := m.sess.BarrierParticipants()
	if len(ps) == 0 {
		m.room = m.room.AppendSystem("[no agents — use /invite <alias> to start one]")
		m.room = m.room.SetComposeValue("")
		return m
	}
	stagedAction := m.toStagedAction(action)
	barrier := barrierAliases(stagedAction, ps)
	b := staging.NewBatch(raw, stagedAction, barrier)
	nextRoom, shouldDispatch := m.room.StageBatchOrDispatch(b, m.stagedSnapshotStatus)
	m.room = nextRoom
	if shouldDispatch {
		if m.stagedHandoffSourcePending() {
			return m
		}
		return m.dispatchRoomStagedBatch()
	}
	return m
}

func barrierAliases(action staging.Action, ps []participant.Participant) []string {
	if action.Kind == staging.ActionSend {
		return action.SendPlan.Targets()
	}
	aliases := make([]string, len(ps))
	for i, p := range ps {
		aliases[i] = p.Alias
	}
	return aliases
}

func (m Model) handleStagedInterrupt() Model {
	if !m.room.HasStagedBatch() {
		return m
	}
	nextRoom, blocked, shouldDispatch := m.room.RequestStagedInterrupt(m.stagedSnapshotStatus)
	m.room = nextRoom
	for _, alias := range blocked {
		if err := m.interpreter.ExecuteLegacy(session.CancelCommand{Alias: alias}); err != nil {
			m.room = m.room.AppendSystem(fmt.Sprintf("error: cancel %q: %v", alias, err))
			continue
		}
		m.room = m.room.AppendSystem("[→ " + alias + "] interrupt requested")
	}
	if shouldDispatch {
		if m.stagedHandoffSourcePending() {
			return m
		}
		return m.dispatchRoomStagedBatch()
	}
	return m
}

func (m Model) maybeAdvanceStagedBatch(e session.Event) Model {
	if !m.room.HasStagedBatch() {
		return m
	}
	staged := m.room.StagedBatch()
	if staged.Action.Kind == staging.ActionHandoff && !m.stagedHandoffSourceReady(staged) {
		return m.handleHandoffEventBeforeSourceReady(staged, e)
	}
	switch e := e.(type) {
	case session.AgentStopped:
		m.room = m.room.MarkStagedDiscarded(e.Alias)
	case session.AgentCrashed:
		m.room = m.room.MarkStagedDiscarded(e.Alias)
	case session.ParticipantStatusChanged, session.AgentStarted:
		// Status changes that may unblock dispatch.
	default:
		return m
	}

	nextRoom, shouldDispatch := m.room.RefreshStagedStatus(m.stagedSnapshotStatus)
	m.room = nextRoom
	if shouldDispatch {
		return m.dispatchRoomStagedBatch()
	}
	return m
}

func (m Model) stagedHandoffSourcePending() bool {
	staged := m.room.StagedBatch()
	return staged != nil && staged.Action.Kind == staging.ActionHandoff &&
		!m.stagedHandoffSourceReady(staged)
}

func (m Model) stagedHandoffSourceReady(staged *staging.Batch) bool {
	p, ok := m.sess.Participant(staged.Action.FromAlias)
	if !ok {
		return false
	}
	return projectedTurnReady(
		p.Status,
		p.TurnID(),
		m.projectedTurnByAlias[staged.Action.FromAlias],
	)
}

func projectedTurnReady(status participant.Status, currentTurn, projectedTurn uint64) bool {
	return status == participant.StatusIdle &&
		(currentTurn == 0 || currentTurn == projectedTurn)
}

func (m Model) handleHandoffEventBeforeSourceReady(staged *staging.Batch, e session.Event) Model {
	var alias string
	switch event := e.(type) {
	case session.AgentStopped:
		alias = event.Alias
	case session.AgentCrashed:
		alias = event.Alias
	default:
		return m
	}
	m.room = m.room.MarkStagedDiscarded(alias)
	if alias == staged.Action.FromAlias || alias == staged.Action.ToAlias {
		return m.dispatchRoomStagedBatch()
	}
	nextRoom, _ := m.room.RefreshStagedStatus(m.stagedSnapshotStatus)
	m.room = nextRoom
	return m
}

func (m Model) maybeAdvanceProjectedHandoff(trigger roomstate.UpdateTrigger) Model {
	if trigger.Kind != roomstate.UpdateTriggerAgentTurnCompleted {
		return m
	}
	p, ok := m.sess.Participant(trigger.Alias)
	if !ok || p.TurnID() != trigger.TurnID {
		return m
	}
	if m.projectedTurnByAlias == nil {
		m.projectedTurnByAlias = make(map[string]uint64)
	}
	m.projectedTurnByAlias[trigger.Alias] = trigger.TurnID

	staged := m.room.StagedBatch()
	if staged == nil || staged.Action.Kind != staging.ActionHandoff {
		return m
	}
	if trigger.Alias != staged.Action.FromAlias {
		return m
	}
	if !m.stagedHandoffSourceReady(staged) {
		return m
	}
	nextRoom, shouldDispatch := m.room.RefreshStagedStatus(m.stagedSnapshotStatus)
	m.room = nextRoom
	if shouldDispatch {
		return m.dispatchRoomStagedBatch()
	}
	return m
}

func (m Model) executeAction(a promptlang.Statement) (Model, tea.Cmd) {
	if out, ok := m.executeAgentAction(a); ok {
		return out, nil
	}
	if out, ok := m.executeDebugAction(a); ok {
		return out, nil
	}
	return m.executeUIAction(a)
}

func (m Model) executeAgentAction(a promptlang.Statement) (Model, bool) {
	switch act := a.(type) {
	case promptlang.Send:
		return m.sendToAgent(act.Alias, act.Text), true
	case promptlang.Broadcast:
		return m.broadcastAll(act.Text), true
	case promptlang.Handoff:
		return m.handoff(act.FromAlias, act.ToAlias), true
	default:
		return m, false
	}
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

func (m Model) executeUIAction(promptlang.Statement) (Model, tea.Cmd) {
	return m, nil
}

func (m Model) executeHandoff(fromAlias, toAlias string, idleAliases []string) (Model, []string, error) {
	source, _ := m.room.LatestHandoffSource(fromAlias)
	err := m.interpreter.ExecuteLegacy(session.HandoffCommand{
		FromAlias:   fromAlias,
		ToAlias:     toAlias,
		IdleAliases: append([]string(nil), idleAliases...),
		Source:      source,
	})
	if err != nil {
		m.room = m.room.AppendSystem(fmt.Sprintf("error: handoff %q -> %q: %v", fromAlias, toAlias, err))
		return m, nil, fmt.Errorf("handoff %q -> %q: %w", fromAlias, toAlias, err)
	}
	if fromAlias == toAlias {
		return m, []string{fromAlias}, nil
	}
	return m, []string{fromAlias, toAlias}, nil
}

func (m Model) handoff(fromAlias, toAlias string) Model {
	var idleAliases []string
	for _, p := range m.sess.BarrierParticipants() {
		idleAliases = append(idleAliases, p.Alias)
	}
	m, _, _ = m.executeHandoff(fromAlias, toAlias, idleAliases)
	return m
}

func (m Model) executeSendToAgent(alias, text string) (Model, []string, error) {
	return m.executePlannedSendToAgent(m.sess.PlanSharedSend(alias), text)
}

func (m Model) executePlannedSendToAgent(plan session.SharedSendPlan, text string) (Model, []string, error) {
	targets := plan.Targets()
	if len(targets) == 0 {
		m.room = m.room.AppendSystem("error: invalid shared send plan")
		return m, nil, fmt.Errorf("invalid shared send plan")
	}
	alias := targets[0]
	err := m.interpreter.ExecuteLegacy(session.SharedSendCommand{
		Plan:          plan,
		TextDirect:    text,
		TextListeners: fmt.Sprintf("@%s: %s", alias, text),
	})
	if err != nil {
		m.room = m.room.AppendSystem(fmt.Sprintf("error: send to %q: %v", alias, err))
		return m, session.DeliveredAliases(err), fmt.Errorf("send to %q: %w", alias, err)
	}
	return m, targets, nil
}

func (m Model) sendToAgent(alias, text string) Model {
	m, _, _ = m.executeSendToAgent(alias, text)
	return m
}

func (m Model) executeBroadcastAll(text string) (Model, []string, error) {
	if len(m.sess.RoutableParticipants()) == 0 {
		m.room = m.room.AppendSystem("[no agents — use /invite <alias> to start one]")
		return m, nil, fmt.Errorf("no routable agents")
	}
	if err := m.interpreter.ExecuteLegacy(session.BroadcastCommand{Text: text}); err != nil {
		m.room = m.room.AppendSystem(fmt.Sprintf("error: broadcast: %v", err))
		return m, session.DeliveredAliases(err), fmt.Errorf("broadcast: %w", err)
	}
	return m, m.routingFor(promptlang.Broadcast{Text: text}), nil
}

func (m Model) broadcastAll(text string) Model {
	m, _, _ = m.executeBroadcastAll(text)
	return m
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
