package ui

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/roomscript/coderoom/internal/agent"
	roomconfig "github.com/roomscript/coderoom/internal/config"
	"github.com/roomscript/coderoom/internal/interpreter"
	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/session"
	"github.com/roomscript/coderoom/internal/ui/room/history/record"
)

type testAgent struct {
	ch        chan agent.Message
	sendErr   error
	noticeErr error
	sendCalls int
	sent      []string
}

type gateStartAgent struct {
	*testAgent
	startGate chan struct{}
}

func newTestAgent() *testAgent {
	return &testAgent{ch: make(chan agent.Message, 16)}
}

func (a *testAgent) Start() error { return nil }

const testTurnAnchor = agent.StreamID("test:turn-anchor")

func (a *testAgent) Send(text string) (agent.StreamID, error) {
	a.sendCalls++
	a.sent = append(a.sent, text)
	if a.sendErr != nil {
		return "", a.sendErr
	}
	return testTurnAnchor, nil
}
func (a *testAgent) SendNotice(string) (agent.StreamID, error) {
	if a.noticeErr != nil {
		return "", a.noticeErr
	}
	// In this test stub, treat notices as a complete turn that ends immediately.
	const noticeTurnStream = agent.StreamID("codex:notice-turn")
	a.push(agent.Message{StreamID: noticeTurnStream, Mode: agent.ModeFlush, Content: agent.Output{}})
	return noticeTurnStream, nil
}
func (a *testAgent) Interrupt() error { return nil }
func (a *testAgent) Stop() error {
	close(a.ch)
	return nil
}
func (a *testAgent) Read() (agent.Message, error) {
	msg, ok := <-a.ch
	if !ok {
		return agent.Message{}, errors.New("agent closed")
	}
	return msg, nil
}

func (a *testAgent) push(msg agent.Message) { a.ch <- msg }

func (a *gateStartAgent) Start() error {
	<-a.startGate
	return a.testAgent.Start()
}

func mustPullEvent(t *testing.T, m *Model) session.Event {
	t.Helper()
	ch := make(chan session.Event, 1)
	go func() {
		ev, ok := sessionOracles[m.interpreter].Pull()
		if !ok {
			return
		}
		ch <- ev
	}()
	select {
	case ev := <-ch:
		return ev
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for session event")
		return nil
	}
}

func pumpUntil(t *testing.T, m Model, pred func(session.Event) bool) Model {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		ev := mustPullEvent(t, &m)
		if pred(ev) {
			return m
		}
	}
	t.Fatal("timed out pumping events")
	return m
}

func inviteParticipant(t *testing.T, s *session.Session, alias string) {
	t.Helper()
	if err := s.Execute(session.InviteCommand{Alias: alias}); err != nil {
		t.Fatalf("invite %s: %v", alias, err)
	}
}

func pumpUntilAgentsStarted(t *testing.T, m Model, want ...string) Model {
	t.Helper()
	started := map[string]bool{}
	wantSet := map[string]bool{}
	for _, alias := range want {
		wantSet[alias] = true
	}
	return pumpUntil(t, m, func(ev session.Event) bool {
		if startedEv, ok := ev.(session.AgentStarted); ok {
			started[startedEv.Alias] = true
		}
		for alias := range wantSet {
			if !started[alias] {
				return false
			}
		}
		return true
	})
}

func assertHistoryDoesNotContainUserInput(t *testing.T, m Model, text string) {
	t.Helper()
	for _, r := range m.room.HistoryRecords() {
		if r.Kind == record.KindUserInput && r.Text == text {
			t.Fatalf("did not expect user input %q to be committed before dispatch", text)
		}
	}
}

func assertHistoryContainsUserInput(t *testing.T, m Model, text string) {
	t.Helper()
	for _, r := range m.room.HistoryRecords() {
		if r.Kind == record.KindUserInput && r.Text == text {
			return
		}
	}
	t.Fatalf("expected committed user input record after dispatch; records: %v", m.room.HistoryRecords())
}

func assertHistoryContainsSystem(t *testing.T, m Model, text string) {
	t.Helper()
	if !hasRecord(m, record.KindSystem, text) {
		t.Fatalf("expected system record containing %q; records: %v", text, m.room.HistoryRecords())
	}
}

func isIdleStatusChange(alias string) func(session.Event) bool {
	return func(ev session.Event) bool {
		status, ok := ev.(session.ParticipantStatusChanged)
		return ok &&
			status.Alias == alias &&
			status.From == participant.StatusWorking &&
			status.To == participant.StatusIdle
	}
}

func isStreamOutput(alias, text string) func(session.Event) bool {
	return func(ev session.Event) bool {
		msg, ok := ev.(session.AgentMessage)
		if !ok || msg.Alias != alias {
			return false
		}
		out, ok := msg.Msg.Content.(agent.Output)
		return ok && msg.Msg.Mode == agent.ModeStream && out.Text == text
	}
}

func isHandoff(fromAlias, toAlias string) func(session.Event) bool {
	return func(ev session.Event) bool {
		handoff, ok := ev.(session.ContextHandoff)
		return ok &&
			handoff.FromAlias == fromAlias &&
			handoff.ToAlias == toAlias
	}
}

func stageHandoffWithCompletedAdaOutput(t *testing.T, agents map[string]agent.Agent, started ...string) (*testAgent, *session.Session, Model) {
	t.Helper()

	ada := agents["ada"].(*testAgent)
	s := session.New(session.WithAgentFactory(func(_ *session.Session, cfg roomconfig.ParticipantConfig, _ session.AgentBackend) agent.Agent {
		return agents[cfg.Alias]
	}))
	t.Cleanup(s.Shutdown)
	m := newTestModelWithSession(t, s)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = next.(Model)

	inviteParticipant(t, s, "ada")
	inviteParticipant(t, s, "turing")
	for _, alias := range started {
		if alias == "ada" || alias == "turing" {
			continue
		}
		inviteParticipant(t, s, alias)
	}
	m = pumpUntilAgentsStarted(t, m, "ada", "turing")

	if err := s.Execute(session.PrivateSendCommand{Alias: "ada", Text: "seed handoff source"}); err != nil {
		t.Fatalf("seed handoff source: %v", err)
	}
	ada.push(agent.Message{
		StreamID: "completed-output",
		Mode:     agent.ModeSingle,
		Content:  agent.Output{Text: "prior completed output"},
	})
	ada.push(agent.Message{StreamID: testTurnAnchor, Mode: agent.ModeFlush, Content: agent.Output{}})
	m = pumpUntil(t, m, func(ev session.Event) bool {
		msg, ok := ev.(session.AgentMessage)
		return ok && msg.Alias == "ada" && msg.TurnCompleted
	})

	return ada, s, m
}

func TestBarrierBatch_stagesThenDispatchesWhenIdle(t *testing.T) {
	agents := map[string]*testAgent{
		"ada":    newTestAgent(),
		"turing": newTestAgent(),
	}
	s := session.New(session.WithAgentFactory(func(_ *session.Session, cfg roomconfig.ParticipantConfig, _ session.AgentBackend) agent.Agent {
		return agents[cfg.Alias]
	}))
	t.Cleanup(s.Shutdown)
	m := newTestModelWithSession(t, s)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = next.(Model)

	// Invite two participants and pump until both are started.
	inviteParticipant(t, s, "ada")
	inviteParticipant(t, s, "turing")
	m = pumpUntilAgentsStarted(t, m, "ada", "turing")

	// Mark ada working, leaving turing idle.
	if err := s.Execute(session.PrivateSendCommand{Alias: "ada", Text: "busy"}); err != nil {
		t.Fatalf("make ada busy: %v", err)
	}
	p, _ := s.Participant("ada")
	if p.Status != participant.StatusWorking {
		t.Fatalf("expected ada to be working, got %q", p.Status)
	}

	// Submit a broadcast; should stage, not dispatch.
	next = submitThroughInterpreter(t, m, "next turn")
	m = next.(Model)
	m = consumeInterpreterStateChange(t, m)
	if m.interpreter.Snapshot().Stage == nil || !m.room.IsComposerStaged() {
		t.Fatalf("expected interpreter-owned stage and staged composer")
	}
	assertHistoryDoesNotContainUserInput(t, m, "next turn")

	// Signal turn completion for ada and pump events until it becomes idle.
	agents["ada"].push(agent.Message{StreamID: "out1", Mode: agent.ModeStream, Content: agent.Output{Text: "done"}})
	agents["ada"].push(agent.Message{StreamID: "out1", Mode: agent.ModeFlush, Content: agent.Output{}})
	agents["ada"].push(agent.Message{StreamID: testTurnAnchor, Mode: agent.ModeFlush, Content: agent.Output{}})
	m = pumpUntil(t, m, isIdleStatusChange("ada"))
	m = consumeInterpreterUntil(t, m, func(event interpreter.Event) bool {
		_, ok := event.(interpreter.StagedInputDispatched)
		return ok
	})
	m = consumeInterpreterStateChange(t, m)

	if m.interpreter.Snapshot().Stage != nil || m.room.IsComposerStaged() {
		t.Fatalf("expected staged batch cleared after dispatch")
	}
	assertHistoryContainsUserInput(t, m, "next turn")
}

func TestBarrierBatch_directSendIgnoresUnrelatedBusyParticipantByDefault(t *testing.T) {
	agents := map[string]*testAgent{
		"ada":    newTestAgent(),
		"turing": newTestAgent(),
	}
	s := session.New(session.WithAgentFactory(func(_ *session.Session, cfg roomconfig.ParticipantConfig, _ session.AgentBackend) agent.Agent {
		return agents[cfg.Alias]
	}))
	t.Cleanup(s.Shutdown)
	m := newTestModelWithSession(t, s)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = next.(Model)

	inviteParticipant(t, s, "ada")
	inviteParticipant(t, s, "turing")
	m = pumpUntilAgentsStarted(t, m, "ada", "turing")
	if err := s.Execute(session.PrivateSendCommand{Alias: "turing", Text: "busy"}); err != nil {
		t.Fatalf("make turing busy: %v", err)
	}

	next = submitThroughInterpreter(t, m, "@ada do it")
	m = next.(Model)

	if m.interpreter.Snapshot().Stage != nil {
		t.Fatal("direct send should not wait for an unrelated participant")
	}
	if agents["ada"].sendCalls != 1 {
		t.Fatalf("ada send calls = %d, want 1", agents["ada"].sendCalls)
	}
	assertHistoryContainsUserInput(t, m, "@ada do it")
}

func TestBarrierBatch_sendNoticesPolicyIncludesBusyListener(t *testing.T) {
	agents := map[string]*testAgent{
		"ada":    newTestAgent(),
		"ben":    newTestAgent(),
		"turing": newTestAgent(),
	}
	s := session.New(session.WithAgentFactory(func(_ *session.Session, cfg roomconfig.ParticipantConfig, _ session.AgentBackend) agent.Agent {
		return agents[cfg.Alias]
	}))
	t.Cleanup(s.Shutdown)
	m := newTestModelWithSession(t, s)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = next.(Model)

	inviteParticipant(t, s, "ada")
	inviteParticipant(t, s, "turing")
	m = pumpUntilAgentsStarted(t, m, "ada", "turing")
	next = submitThroughInterpreter(t, m, "/policy enable send-notices")
	m = next.(Model)
	assertHistoryContainsSystem(t, m, "[policy] send-notices enabled")
	if err := s.Execute(session.PrivateSendCommand{Alias: "turing", Text: "busy"}); err != nil {
		t.Fatalf("make turing busy: %v", err)
	}

	next = submitThroughInterpreter(t, m, "@ada do it")
	m = next.(Model)
	m = consumeInterpreterStateChange(t, m)

	if m.interpreter.Snapshot().Stage == nil || !m.room.IsComposerStaged() {
		t.Fatal("direct send should wait for a listener when send-notices is enabled")
	}
	stage := m.interpreter.Snapshot().Stage
	if stage == nil || !slices.Equal(stage.Routing, []string{"ada", "turing"}) {
		t.Fatalf("stage = %#v, want frozen routing [ada turing]", stage)
	}

	inviteParticipant(t, s, "ben")
	m = pumpUntilAgentsStarted(t, m, "ben")
	if got := m.interpreter.Snapshot().Stage.Routing; !slices.Equal(got, []string{"ada", "turing"}) {
		t.Fatalf("stage routing after ben starts = %v, want [ada turing]", got)
	}
}

func TestBarrierBatch_autoDispatchPreservesFirstOutputRecord(t *testing.T) {
	agents, s, m := newTwoAgentBarrierBatchModel(t)

	if err := s.Execute(session.SharedSendCommand{Plan: s.PlanSharedSend("ada"), TextDirect: "busy", TextListeners: "notice"}); err != nil {
		t.Fatalf("make ada busy: %v", err)
	}
	m = submitThroughInterpreter(t, m, "next turn")
	m = consumeInterpreterStateChange(t, m)
	if m.interpreter.Snapshot().Stage == nil || !m.room.IsComposerStaged() {
		t.Fatal("expected interpreter-owned stage before ada becomes idle")
	}

	agents["ada"].push(agent.Message{
		StreamID: "turn-old-output",
		Mode:     agent.ModeStream,
		Content:  agent.Output{Text: "old output"},
	})
	agents["ada"].push(agent.Message{StreamID: "turn-old-output", Mode: agent.ModeFlush, Content: agent.Output{}})
	agents["ada"].push(agent.Message{StreamID: testTurnAnchor, Mode: agent.ModeFlush, Content: agent.Output{}})
	m = pumpUntil(t, m, isIdleStatusChange("ada"))
	m = consumeInterpreterUntil(t, m, func(event interpreter.Event) bool {
		_, ok := event.(interpreter.StagedInputDispatched)
		return ok
	})
	m = consumeInterpreterStateChange(t, m)

	agents["ada"].push(agent.Message{
		StreamID: "turn-new-output",
		Mode:     agent.ModeStream,
		Content:  agent.Output{Text: "fresh output"},
	})
	m = pumpUntil(t, m, isStreamOutput("ada", "fresh output"))
	m = consumeInterpreterPresentation(t, m, func(next Model) bool { return hasRecord(next, record.KindAgentOutput, "fresh output") })

	if !hasRecord(m, record.KindAgentOutput, "fresh output") {
		t.Fatalf("expected first output fragment from auto-dispatched turn to appear in history; records: %v", m.room.HistoryRecords())
	}
}

func TestBarrierBatch_failedDispatchDoesNotCommitUserInput(t *testing.T) {
	agents := map[string]*testAgent{
		"ada": newTestAgent(),
	}
	agents["ada"].sendErr = errors.New("send failed")

	s := session.New(session.WithAgentFactory(func(_ *session.Session, cfg roomconfig.ParticipantConfig, _ session.AgentBackend) agent.Agent {
		return agents[cfg.Alias]
	}))
	t.Cleanup(s.Shutdown)
	m := newTestModelWithSession(t, s)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = next.(Model)

	inviteParticipant(t, s, "ada")
	m = pumpUntilAgentsStarted(t, m, "ada")

	next = submitThroughInterpreter(t, m, "next turn")
	m = next.(Model)

	assertHistoryDoesNotContainUserInput(t, m, "next turn")
	if !hasRecord(m, record.KindSystem, `error: staged dispatch: execute broadcast: broadcast to "ada"`) {
		t.Fatalf("expected dispatch error to be surfaced; records: %v", m.room.HistoryRecords())
	}
	if m.room.ComposeValue() != "next turn" {
		t.Fatalf("expected failed dispatch to preserve composer text, got %q", m.room.ComposeValue())
	}
	if m.interpreter.Snapshot().Stage != nil || m.room.IsComposerStaged() {
		t.Fatal("expected failed dispatch to clear staged state")
	}
}

func TestBarrierBatch_failedDispatchDoesNotRetryOnRollbackIdle(t *testing.T) {
	agents := map[string]*testAgent{
		"ada": newTestAgent(),
	}
	agents["ada"].sendErr = errors.New("send failed")

	s := session.New(session.WithAgentFactory(func(_ *session.Session, cfg roomconfig.ParticipantConfig, _ session.AgentBackend) agent.Agent {
		return agents[cfg.Alias]
	}))
	t.Cleanup(s.Shutdown)
	m := newTestModelWithSession(t, s)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = next.(Model)

	inviteParticipant(t, s, "ada")
	m = pumpUntilAgentsStarted(t, m, "ada")

	next = submitThroughInterpreter(t, m, "next turn")
	m = next.(Model)
	if agents["ada"].sendCalls != 1 {
		t.Fatalf("expected initial dispatch attempt count 1, got %d", agents["ada"].sendCalls)
	}

	for range 3 {
		_ = mustPullEvent(t, &m)
	}
	m = consumeInterpreterStateChange(t, m)

	if agents["ada"].sendCalls != 1 {
		t.Fatalf("expected no retry after rollback idle events, got %d send attempts", agents["ada"].sendCalls)
	}
	if m.interpreter.Snapshot().Stage != nil || m.room.IsComposerStaged() {
		t.Fatal("expected staged state to remain cleared after rollback events")
	}
}

func TestBarrierBatch_partialDispatchCommitsUserInput(t *testing.T) {
	agents := map[string]*testAgent{
		"ada":    newTestAgent(),
		"turing": newTestAgent(),
	}
	agents["ada"].sendErr = errors.New("send failed")

	s := session.New(session.WithAgentFactory(func(_ *session.Session, cfg roomconfig.ParticipantConfig, _ session.AgentBackend) agent.Agent {
		return agents[cfg.Alias]
	}))
	t.Cleanup(s.Shutdown)
	m := newTestModelWithSession(t, s)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = next.(Model)

	inviteParticipant(t, s, "ada")
	inviteParticipant(t, s, "turing")
	m = pumpUntilAgentsStarted(t, m, "ada", "turing")

	next = submitThroughInterpreter(t, m, "next turn")
	m = next.(Model)

	assertHistoryContainsUserInput(t, m, "next turn")
	if !hasRecord(m, record.KindSystem, `error: staged dispatch: execute broadcast: broadcast to "ada"`) {
		t.Fatalf("expected dispatch error to be surfaced; records: %v", m.room.HistoryRecords())
	}
	if m.room.ComposeValue() != "" {
		t.Fatalf("expected partial dispatch to clear composer, got %q", m.room.ComposeValue())
	}
	if m.interpreter.Snapshot().Stage != nil {
		t.Fatal("expected staged batch cleared after partial dispatch")
	}
}

func TestBarrierBatch_discardedTargetRestoresDraft(t *testing.T) {
	agents := map[string]*testAgent{
		"ada": newTestAgent(),
	}

	s := session.New(session.WithAgentFactory(func(_ *session.Session, cfg roomconfig.ParticipantConfig, _ session.AgentBackend) agent.Agent {
		return agents[cfg.Alias]
	}))
	t.Cleanup(s.Shutdown)
	m := newTestModelWithSession(t, s)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = next.(Model)

	inviteParticipant(t, s, "ada")
	m = pumpUntilAgentsStarted(t, m, "ada")

	if err := s.Execute(session.PrivateSendCommand{Alias: "ada", Text: "busy"}); err != nil {
		t.Fatalf("make ada busy: %v", err)
	}
	next = submitThroughInterpreter(t, m, "@ada hi")
	m = next.(Model)
	m = consumeInterpreterStateChange(t, m)
	if m.interpreter.Snapshot().Stage == nil || !m.room.IsComposerStaged() {
		t.Fatal("expected staged batch before target disappears")
	}

	if err := s.Execute(session.RemoveCommand{Alias: "ada"}); err != nil {
		t.Fatalf("remove ada: %v", err)
	}
	m = pumpUntil(t, m, func(ev session.Event) bool {
		stopped, ok := ev.(session.AgentStopped)
		return ok && stopped.Alias == "ada"
	})

	m = consumeInterpreterPresentation(t, m, func(next Model) bool {
		return stagedDiscardPresented(next, "staged message discarded: no active targets")
	})
	assertHistoryDoesNotContainUserInput(t, m, "@ada hi")
	if !hasRecord(m, record.KindSystem, "staged message discarded: no active targets") {
		t.Fatalf("expected staged discard message; records: %v", m.room.HistoryRecords())
	}
	if m.room.ComposeValue() != "@ada hi" {
		t.Fatalf("expected discarded staged send to restore draft, got %q", m.room.ComposeValue())
	}
	if m.interpreter.Snapshot().Stage != nil || m.room.IsComposerStaged() {
		t.Fatal("expected staged state cleared after target discard")
	}
}

func TestBarrierBatch_handoffIgnoresStartingBystanderOutsideBarrier(t *testing.T) {
	agents := map[string]agent.Agent{
		"ada":    newTestAgent(),
		"turing": newTestAgent(),
		"cat":    &gateStartAgent{testAgent: newTestAgent(), startGate: make(chan struct{})},
	}
	cat := agents["cat"].(*gateStartAgent)
	ada, s, m := stageHandoffWithCompletedAdaOutput(t, agents, "cat")
	if err := s.Execute(session.SharedSendCommand{Plan: s.PlanSharedSend("ada"), TextDirect: "busy", TextListeners: "notice"}); err != nil {
		t.Fatalf("make ada busy: %v", err)
	}
	m = submitThroughInterpreter(t, m, "/handoff ada turing")
	m = consumeInterpreterStateChange(t, m)
	assertInterpreterHandoffStaged(t, m)
	ada.push(agent.Message{StreamID: testTurnAnchor, Mode: agent.ModeFlush, Content: agent.Output{}})
	m = pumpUntil(t, m, isHandoff("ada", "turing"))
	m = consumeInterpreterHandoffDispatch(t, m)

	if m.interpreter.Snapshot().Stage != nil || m.room.IsComposerStaged() {
		t.Fatal("expected staged handoff cleared after dispatch")
	}
	assertHistoryContainsUserInput(t, m, "/handoff ada turing")
	if !hasRecord(m, record.KindSystem, "[handoff ada -> turing]") {
		t.Fatalf("expected handoff record after dispatch; records: %v", m.room.HistoryRecords())
	}
	assertRecordOrder(t, m, "/handoff ada turing", "[handoff ada -> turing]")

	close(cat.startGate)
}

func assertRecordOrder(t *testing.T, m Model, first, second string) {
	t.Helper()
	firstIndex, secondIndex := -1, -1
	for index, item := range m.room.HistoryRecords() {
		if strings.HasPrefix(item.Text, first) && firstIndex < 0 {
			firstIndex = index
		}
		if strings.HasPrefix(item.Text, second) && secondIndex < 0 {
			secondIndex = index
		}
	}
	if firstIndex < 0 || secondIndex < 0 || firstIndex >= secondIndex {
		t.Fatalf("record order %q before %q not preserved: %v", first, second, m.room.HistoryRecords())
	}
}

func TestInterpreterHandoffSendNoticeFailureDoesNotCommitInput(t *testing.T) {
	agents := map[string]agent.Agent{
		"ada":    newTestAgent(),
		"turing": newTestAgent(),
	}
	_, _, m := stageHandoffWithCompletedAdaOutput(t, agents)
	agents["turing"].(*testAgent).noticeErr = errors.New("notice failed")

	m = submitThroughInterpreter(t, m, "/handoff ada turing")
	m = consumeInterpreterStateChange(t, m)

	assertHistoryDoesNotContainUserInput(t, m, "/handoff ada turing")
	if got := m.room.ComposeValue(); got != "/handoff ada turing" {
		t.Fatalf("composer = %q, want restored handoff", got)
	}
	if !hasRecord(m, record.KindLog, "handoff rejected:") {
		t.Fatalf("expected rejected handoff audit log; records: %v", m.room.HistoryRecords())
	}
}

func TestBarrierBatch_handoffDispatchRacesSourceFlush(t *testing.T) {
	// Regression test for staged /handoff dispatch racing room-state finalization.
	//
	// The broken ordering was:
	// 1. session.handleAgentMessage processes the anchor Output+ModeFlush
	// 2. applyTurnLifecycle marks the source participant idle
	// 3. the UI auto-dispatches the staged handoff on that idle event
	// 4. only after that does the room ingest the final AgentMessage update
	//
	// When the source output lives on the anchor stream itself,
	// LatestCompletedOutput still sees an open ModeStream record at dispatch
	// time, so /handoff resolves "no completed room-visible output" even though
	// the source turn has just finished. The fix is to defer handoff dispatch
	// on the source's idle status event and re-check on the following
	// AgentMessage after the room update has been applied.
	agents, s, m := newTwoAgentBarrierBatchModel(t)
	m = stageBusyHandoff(t, s, m)
	pushAnchorOutput(agents["ada"], "fresh output")
	m = waitForHandoffEvent(t, m)
	assertHandoffRaceResolved(t, m)
}

func TestBarrierBatch_nonCompletingFlushDoesNotSatisfyHandoffSourceReadiness(t *testing.T) {
	agents, s, m := newTwoAgentBarrierBatchModel(t)
	m = stageBusyHandoff(t, s, m)
	agents["ada"].push(agent.Message{
		StreamID: "reasoning",
		Mode:     agent.ModeStream,
		Content:  agent.Reasoning{Text: "thinking"},
	})
	agents["ada"].push(agent.Message{
		StreamID: "reasoning",
		Mode:     agent.ModeFlush,
		Content:  agent.Reasoning{},
	})
	m = pumpUntil(t, m, func(event session.Event) bool {
		message, ok := event.(session.AgentMessage)
		return ok && message.Msg.StreamID == "reasoning" && message.Msg.Mode == agent.ModeFlush
	})

	if m.interpreter.Snapshot().Stage == nil {
		t.Fatal("non-completing reasoning flush dispatched staged handoff")
	}

	pushAnchorOutput(agents["ada"], "fresh output")
	m = waitForHandoffEvent(t, m)
	assertHandoffRaceResolved(t, m)
}

func newTwoAgentBarrierBatchModel(t *testing.T) (map[string]*testAgent, *session.Session, Model) {
	t.Helper()

	agents := map[string]*testAgent{
		"ada":    newTestAgent(),
		"turing": newTestAgent(),
	}
	s := session.New(session.WithAgentFactory(func(_ *session.Session, cfg roomconfig.ParticipantConfig, _ session.AgentBackend) agent.Agent {
		return agents[cfg.Alias]
	}))
	t.Cleanup(s.Shutdown)
	m := newTestModelWithSession(t, s)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = next.(Model)

	inviteParticipant(t, s, "ada")
	inviteParticipant(t, s, "turing")
	return agents, s, pumpUntilAgentsStarted(t, m, "ada", "turing")
}

func stageBusyHandoff(t *testing.T, s *session.Session, m Model) Model {
	t.Helper()

	if err := s.Execute(session.PrivateSendCommand{Alias: "ada", Text: "busy"}); err != nil {
		t.Fatalf("make ada busy: %v", err)
	}
	m = submitThroughInterpreter(t, m, "/handoff ada turing")
	m = consumeInterpreterStateChange(t, m)
	assertInterpreterHandoffStaged(t, m)
	return m
}

func assertInterpreterHandoffStaged(t *testing.T, m Model) {
	t.Helper()
	if m.interpreter.Snapshot().Stage == nil || !m.room.IsComposerStaged() {
		t.Fatal("expected interpreter-owned staged handoff")
	}
}

func consumeInterpreterHandoffDispatch(t *testing.T, m Model) Model {
	t.Helper()
	m = consumeInterpreterUntil(t, m, func(event interpreter.Event) bool {
		_, ok := event.(interpreter.StagedInputDispatched)
		return ok
	})
	return consumeInterpreterStateChange(t, m)
}

func pushAnchorOutput(a *testAgent, text string) {
	a.push(agent.Message{
		StreamID: testTurnAnchor,
		Mode:     agent.ModeStream,
		Content:  agent.Output{Text: text},
	})
	a.push(agent.Message{
		StreamID: testTurnAnchor,
		Mode:     agent.ModeFlush,
		Content:  agent.Output{},
	})
}

func waitForHandoffEvent(t *testing.T, m Model) Model {
	t.Helper()
	return consumeInterpreterHandoffDispatch(t, m)
}

func assertHandoffRaceResolved(t *testing.T, m Model) {
	t.Helper()

	if hasRecord(m, record.KindSystem, `error: handoff "ada" -> "turing"`) {
		t.Fatalf("did not expect handoff race error after source anchor flush; records: %v", m.room.HistoryRecords())
	}
	if m.interpreter.Snapshot().Stage != nil || m.room.IsComposerStaged() {
		t.Fatal("expected staged handoff cleared after dispatch")
	}
	assertHistoryContainsUserInput(t, m, "/handoff ada turing")
	if !hasRecord(m, record.KindAgentOutput, "fresh output") {
		t.Fatalf("expected flushed source output in history; records: %v", m.room.HistoryRecords())
	}
	if !hasRecord(m, record.KindSystem, "[handoff ada -> turing]") {
		t.Fatalf("expected handoff audit record after dispatch; records: %v", m.room.HistoryRecords())
	}
}

func stageDiscardedTargetHandoff(t *testing.T) (*testAgent, *session.Session, Model) {
	t.Helper()

	agents := map[string]*testAgent{
		"ada":    newTestAgent(),
		"turing": newTestAgent(),
	}
	s := session.New(session.WithAgentFactory(func(_ *session.Session, cfg roomconfig.ParticipantConfig, _ session.AgentBackend) agent.Agent {
		return agents[cfg.Alias]
	}))
	t.Cleanup(s.Shutdown)
	m := newTestModelWithSession(t, s)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = next.(Model)

	inviteParticipant(t, s, "ada")
	inviteParticipant(t, s, "turing")
	m = pumpUntilAgentsStarted(t, m, "ada", "turing")

	m = pushEvent(m, session.AgentMessage{Alias: "ada", Msg: agent.Message{
		StreamID: "completed-output",
		Mode:     agent.ModeSingle,
		Content:  agent.Output{Text: "prior completed output"},
	}})

	if err := s.Execute(session.PrivateSendCommand{Alias: "ada", Text: "busy"}); err != nil {
		t.Fatalf("make ada busy: %v", err)
	}
	next = submitThroughInterpreter(t, m, "/handoff ada turing")
	m = next.(Model)
	m = consumeInterpreterStateChange(t, m)
	assertInterpreterHandoffStaged(t, m)

	return agents["ada"], s, m
}

func TestBarrierBatch_handoffDiscardedTargetRestoresDraft(t *testing.T) {
	ada, s, m := stageDiscardedTargetHandoff(t)

	if err := s.Execute(session.RemoveCommand{Alias: "turing"}); err != nil {
		t.Fatalf("remove turing: %v", err)
	}
	m = pumpUntil(t, m, func(ev session.Event) bool {
		stopped, ok := ev.(session.AgentStopped)
		return ok && stopped.Alias == "turing"
	})
	ada.push(agent.Message{StreamID: testTurnAnchor, Mode: agent.ModeFlush, Content: agent.Output{}})
	m = pumpUntil(t, m, func(ev session.Event) bool {
		msg, ok := ev.(session.AgentMessage)
		return ok && msg.Alias == "ada"
	})

	m = consumeInterpreterPresentation(t, m, func(next Model) bool {
		return !next.room.IsComposerStaged() && hasRecord(next, record.KindSystem, `staged message discarded: "turing" is no longer available`)
	})
	assertHistoryDoesNotContainUserInput(t, m, "/handoff ada turing")
	if !hasRecord(m, record.KindSystem, `staged message discarded: "turing" is no longer available`) {
		t.Fatalf("expected staged handoff discard message; records: %v", m.room.HistoryRecords())
	}
	if m.room.ComposeValue() != "/handoff ada turing" {
		t.Fatalf("expected discarded staged handoff to restore draft, got %q", m.room.ComposeValue())
	}
	if m.interpreter.Snapshot().Stage != nil || m.room.IsComposerStaged() {
		t.Fatal("expected staged state cleared after handoff target discard")
	}
}

func TestBarrierBatch_handoffIgnoresBusyParticipantWhoJoinedAfterStaging(t *testing.T) {
	agents := map[string]agent.Agent{
		"ada":    newTestAgent(),
		"turing": newTestAgent(),
		"cat":    newTestAgent(),
	}
	ada, s, m := stageHandoffWithCompletedAdaOutput(t, agents)
	if err := s.Execute(session.PrivateSendCommand{Alias: "ada", Text: "busy"}); err != nil {
		t.Fatalf("make ada busy: %v", err)
	}
	m = submitThroughInterpreter(t, m, "/handoff ada turing")
	m = consumeInterpreterStateChange(t, m)
	assertInterpreterHandoffStaged(t, m)

	inviteParticipant(t, s, "cat")
	m = pumpUntilAgentsStarted(t, m, "cat")
	if err := s.Execute(session.PrivateSendCommand{Alias: "cat", Text: "busy"}); err != nil {
		t.Fatalf("make cat busy: %v", err)
	}

	ada.push(agent.Message{StreamID: testTurnAnchor, Mode: agent.ModeFlush, Content: agent.Output{}})
	m = pumpUntil(t, m, isHandoff("ada", "turing"))
	m = consumeInterpreterHandoffDispatch(t, m)

	if hasRecord(m, record.KindSystem, `error: handoff "ada" -> "turing"`) {
		t.Fatalf("did not expect busy late joiner to block staged handoff; records: %v", m.room.HistoryRecords())
	}
	if !hasRecord(m, record.KindSystem, "[handoff ada -> turing]") {
		t.Fatalf("expected handoff record after dispatch; records: %v", m.room.HistoryRecords())
	}
}

func stagedDiscardPresented(m Model, reason string) bool {
	return !m.room.IsComposerStaged() && hasRecord(m, record.KindSystem, reason)
}
