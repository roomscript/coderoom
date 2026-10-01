package ui

import (
	"errors"
	"slices"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/trigosec/coderoom/internal/agent"
	roomconfig "github.com/trigosec/coderoom/internal/config"
	"github.com/trigosec/coderoom/internal/interpreter"
	"github.com/trigosec/coderoom/internal/participant"
	roomstate "github.com/trigosec/coderoom/internal/room"
	"github.com/trigosec/coderoom/internal/session"
	"github.com/trigosec/coderoom/internal/ui/room/history/record"
)

type testAgent struct {
	ch        chan agent.Message
	sendErr   error
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
		ev, ok := m.queue.Pull()
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
		next, _ := m.Update(sessionEventMsg{event: ev})
		m = next.(Model)
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
	if m.room.HasStagedBatch() || !m.room.IsComposerStaged() {
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

	if m.room.HasStagedBatch() || m.room.IsComposerStaged() {
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

	if m.room.HasStagedBatch() {
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

	if m.room.HasStagedBatch() || !m.room.IsComposerStaged() {
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

	if err := s.Execute(session.SharedSendCommand{Plan: s.PlanSharedSend("ada"), TextDirect: "busy", TextListeners: "notice"}); err != nil {
		t.Fatalf("make ada busy: %v", err)
	}
	next = submitThroughInterpreter(t, m, "next turn")
	m = next.(Model)
	m = consumeInterpreterStateChange(t, m)
	if m.room.HasStagedBatch() || !m.room.IsComposerStaged() {
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
	if m.room.HasStagedBatch() || m.room.IsComposerStaged() {
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
		ev := mustPullEvent(t, &m)
		next, _ = m.Update(sessionEventMsg{event: ev})
		m = next.(Model)
	}

	if agents["ada"].sendCalls != 1 {
		t.Fatalf("expected no retry after rollback idle events, got %d send attempts", agents["ada"].sendCalls)
	}
	if m.room.HasStagedBatch() || m.room.IsComposerStaged() {
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
	if m.room.HasStagedBatch() {
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
	if m.room.HasStagedBatch() || !m.room.IsComposerStaged() {
		t.Fatal("expected staged batch before target disappears")
	}

	if err := s.Execute(session.RemoveCommand{Alias: "ada"}); err != nil {
		t.Fatalf("remove ada: %v", err)
	}
	m = pumpUntil(t, m, func(ev session.Event) bool {
		stopped, ok := ev.(session.AgentStopped)
		return ok && stopped.Alias == "ada"
	})
	m = consumeInterpreterUntil(t, m, func(event interpreter.Event) bool {
		_, ok := event.(interpreter.StagedInputDiscarded)
		return ok
	})
	m = consumeInterpreterStateChange(t, m)

	assertHistoryDoesNotContainUserInput(t, m, "@ada hi")
	if !hasRecord(m, record.KindSystem, "staged message discarded: no active targets") {
		t.Fatalf("expected staged discard message; records: %v", m.room.HistoryRecords())
	}
	if m.room.ComposeValue() != "@ada hi" {
		t.Fatalf("expected discarded staged send to restore draft, got %q", m.room.ComposeValue())
	}
	if m.room.HasStagedBatch() || m.room.IsComposerStaged() {
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
	if !m.room.HasStagedBatch() {
		t.Fatal("expected staged handoff before ada becomes idle")
	}
	ada.push(agent.Message{StreamID: testTurnAnchor, Mode: agent.ModeFlush, Content: agent.Output{}})
	m = pumpUntil(t, m, isHandoff("ada", "turing"))

	if m.room.HasStagedBatch() || m.room.IsComposerStaged() {
		t.Fatal("expected staged handoff cleared after dispatch")
	}
	assertHistoryContainsUserInput(t, m, "/handoff ada turing")
	if !hasRecord(m, record.KindSystem, "[handoff ada -> turing]") {
		t.Fatalf("expected handoff record after dispatch; records: %v", m.room.HistoryRecords())
	}

	close(cat.startGate)
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

	if _, ok := m.projectedTurnByAlias["ada"]; ok {
		t.Fatal("non-completing reasoning flush advanced projected turn watermark")
	}
	if !m.room.HasStagedBatch() {
		t.Fatal("non-completing reasoning flush dispatched staged handoff")
	}

	pushAnchorOutput(agents["ada"], "fresh output")
	m = waitForHandoffEvent(t, m)
	assertHandoffRaceResolved(t, m)
}

func TestBarrierBatch_handoffReadinessTracksLatestSourceTurn(t *testing.T) {
	agents := map[string]*testAgent{
		"ada":    newTestAgent(),
		"turing": newTestAgent(),
		"cat":    newTestAgent(),
	}
	s := session.New(session.WithAgentFactory(func(_ *session.Session, cfg roomconfig.ParticipantConfig, _ session.AgentBackend) agent.Agent {
		return agents[cfg.Alias]
	}))
	t.Cleanup(s.Shutdown)
	m := newTestModelWithSession(t, s)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = next.(Model)
	for _, alias := range []string{"ada", "turing", "cat"} {
		inviteParticipant(t, s, alias)
	}
	m = pumpUntilAgentsStarted(t, m, "ada", "turing", "cat")
	if err := s.Execute(session.PrivateSendCommand{Alias: "cat", Text: "busy"}); err != nil {
		t.Fatalf("make cat busy: %v", err)
	}
	m = submitThroughInterpreter(t, m, "/handoff ada turing")

	firstTurn := completeSourceTurnBeforeProjection(t, &m, s, agents["ada"], "first")
	m = m.maybeAdvanceProjectedHandoff(completedTurnTrigger(firstTurn))
	if !m.stagedHandoffSourceReady(m.room.StagedBatch()) || !m.room.HasStagedBatch() {
		t.Fatal("first source completion did not leave handoff ready and staged")
	}

	secondTurn := completeSourceTurnBeforeProjection(t, &m, s, agents["ada"], "second")
	m = m.maybeAdvanceProjectedHandoff(completedTurnTrigger(firstTurn))
	if m.stagedHandoffSourceReady(m.room.StagedBatch()) {
		t.Fatal("delayed completion from previous source turn restored readiness")
	}
	m = m.maybeAdvanceProjectedHandoff(completedTurnTrigger(secondTurn))
	if !m.stagedHandoffSourceReady(m.room.StagedBatch()) {
		t.Fatal("latest source turn completion did not restore readiness")
	}
}

func completeSourceTurnBeforeProjection(t *testing.T, m *Model, s *session.Session, a *testAgent, label string) uint64 {
	t.Helper()
	if err := s.Execute(session.PrivateSendCommand{Alias: "ada", Text: label + " turn"}); err != nil {
		t.Fatalf("start %s ada turn: %v", label, err)
	}
	if m.stagedHandoffSourceReady(m.room.StagedBatch()) {
		t.Fatalf("starting %s source turn did not invalidate handoff readiness", label)
	}
	pushAnchorOutput(a, label+" output")
	return waitForAdaIdle(t, m, s)
}

func TestProjectedTurnReady_preservesWatermarkAcrossTemporaryStates(t *testing.T) {
	tests := []struct {
		name          string
		status        participant.Status
		currentTurn   uint64
		projectedTurn uint64
		want          bool
	}{
		{name: "idle projected turn", status: participant.StatusIdle, currentTurn: 4, projectedTurn: 4, want: true},
		{name: "keepalive same turn", status: participant.StatusKeepalive, currentTurn: 4, projectedTurn: 4},
		{name: "idle after keepalive", status: participant.StatusIdle, currentTurn: 4, projectedTurn: 4, want: true},
		{name: "preparing same turn", status: participant.StatusPreparing, currentTurn: 4, projectedTurn: 4},
		{name: "idle after prepare abort", status: participant.StatusIdle, currentTurn: 4, projectedTurn: 4, want: true},
		{name: "working newer turn", status: participant.StatusWorking, currentTurn: 5, projectedTurn: 4},
		{name: "idle newer turn before projection", status: participant.StatusIdle, currentTurn: 5, projectedTurn: 4},
		{name: "initial idle without turn", status: participant.StatusIdle, currentTurn: 0, projectedTurn: 0, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := projectedTurnReady(tt.status, tt.currentTurn, tt.projectedTurn); got != tt.want {
				t.Fatalf("projectedTurnReady() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestProjectedTurnWatermark_rejectsRemovedParticipantCompletion(t *testing.T) {
	agents := map[string]*testAgent{
		"ada":    newTestAgent(),
		"turing": newTestAgent(),
	}
	s := session.New(session.WithAgentFactory(func(_ *session.Session, cfg roomconfig.ParticipantConfig, _ session.AgentBackend) agent.Agent {
		return agents[cfg.Alias]
	}))
	t.Cleanup(s.Shutdown)
	m := newTestModelWithSession(t, s)
	for _, alias := range []string{"ada", "turing"} {
		inviteParticipant(t, s, alias)
	}
	m = pumpUntilAgentsStarted(t, m, "ada", "turing")

	if err := s.Execute(session.PrivateSendCommand{Alias: "ada", Text: "old incarnation"}); err != nil {
		t.Fatalf("start old ada turn: %v", err)
	}
	pushAnchorOutput(agents["ada"], "old output")
	oldTurnID := waitForAdaIdle(t, &m, s)
	if err := s.Execute(session.RemoveCommand{Alias: "ada"}); err != nil {
		t.Fatalf("remove old ada: %v", err)
	}

	agents["ada"] = newTestAgent()
	inviteParticipant(t, s, "ada")
	m = pumpUntilAgentsStarted(t, m, "ada")
	if err := s.Execute(session.PrivateSendCommand{Alias: "ada", Text: "new incarnation"}); err != nil {
		t.Fatalf("start new ada turn: %v", err)
	}
	pushAnchorOutput(agents["ada"], "new output")
	newTurnID := waitForAdaIdle(t, &m, s)
	if newTurnID == oldTurnID {
		t.Fatalf("turn ID reused across alias reincarnation: %d", newTurnID)
	}

	m = m.maybeAdvanceProjectedHandoff(completedTurnTrigger(oldTurnID))
	if got := m.projectedTurnByAlias["ada"]; got != 0 {
		t.Fatalf("delayed old turn advanced watermark to %d", got)
	}
	m = m.maybeAdvanceProjectedHandoff(completedTurnTrigger(newTurnID))
	if got := m.projectedTurnByAlias["ada"]; got != newTurnID {
		t.Fatalf("new turn watermark = %d, want %d", got, newTurnID)
	}
}

func waitForAdaIdle(t *testing.T, m *Model, s *session.Session) uint64 {
	t.Helper()
	for {
		event := mustPullEvent(t, m)
		status, ok := event.(session.ParticipantStatusChanged)
		if !ok || status.Alias != "ada" || status.To != participant.StatusIdle {
			continue
		}
		p, ok := s.Participant("ada")
		if !ok {
			t.Fatal("participant ada disappeared")
		}
		return p.TurnID()
	}
}

func completedTurnTrigger(turn uint64) roomstate.UpdateTrigger {
	return roomstate.UpdateTrigger{
		Kind:     roomstate.UpdateTriggerAgentTurnCompleted,
		Alias:    "ada",
		StreamID: testTurnAnchor,
		TurnID:   turn,
	}
}

func TestBarrierBatch_bystanderDepartureDoesNotBypassHandoffSourceReadiness(t *testing.T) {
	agents := map[string]*testAgent{
		"ada":    newTestAgent(),
		"turing": newTestAgent(),
		"cat":    newTestAgent(),
	}
	s := session.New(session.WithAgentFactory(func(_ *session.Session, cfg roomconfig.ParticipantConfig, _ session.AgentBackend) agent.Agent {
		return agents[cfg.Alias]
	}))
	t.Cleanup(s.Shutdown)
	m := newTestModelWithSession(t, s)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = next.(Model)
	for _, alias := range []string{"ada", "turing", "cat"} {
		inviteParticipant(t, s, alias)
	}
	m = pumpUntilAgentsStarted(t, m, "ada", "turing", "cat")
	m = stageBusyHandoff(t, s, m)

	pushAnchorOutput(agents["ada"], "fresh output")
	for {
		event := mustPullEvent(t, &m)
		status, ok := event.(session.ParticipantStatusChanged)
		if ok && status.Alias == "ada" && status.To == participant.StatusIdle {
			break
		}
	}
	m = m.maybeAdvanceStagedBatch(session.AgentStopped{Alias: "cat"})

	if !m.room.HasStagedBatch() {
		t.Fatal("bystander departure dispatched handoff before source flush projection")
	}
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
	if !m.room.HasStagedBatch() {
		t.Fatal("expected staged handoff before ada becomes idle")
	}
	return m
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

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if ev, ok := m.queue.PullTimeout(10 * time.Millisecond); ok {
			next, _ := m.Update(sessionEventMsg{event: ev})
			m = next.(Model)
			if handoff, ok := ev.(session.ContextHandoff); ok &&
				handoff.FromAlias == "ada" && handoff.ToAlias == "turing" {
				return m
			}
			continue
		}
		if updated, trigger, ok := m.room.WaitObserverUpdateTriggerTimeout(10 * time.Millisecond); ok {
			m.room = updated
			m = m.maybeAdvanceProjectedHandoff(trigger)
		}
	}
	t.Fatalf("timed out waiting for handoff event; staged=%v records=%v", m.room.HasStagedBatch(), m.room.HistoryRecords())
	return m
}

func assertHandoffRaceResolved(t *testing.T, m Model) {
	t.Helper()

	if hasRecord(m, record.KindSystem, `error: handoff "ada" -> "turing"`) {
		t.Fatalf("did not expect handoff race error after source anchor flush; records: %v", m.room.HistoryRecords())
	}
	if m.room.HasStagedBatch() || m.room.IsComposerStaged() {
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
	if !m.room.HasStagedBatch() {
		t.Fatal("expected staged handoff before target disappears")
	}

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

	assertHistoryDoesNotContainUserInput(t, m, "/handoff ada turing")
	if !hasRecord(m, record.KindSystem, `staged message discarded: "turing" is no longer available`) {
		t.Fatalf("expected staged handoff discard message; records: %v", m.room.HistoryRecords())
	}
	if m.room.ComposeValue() != "/handoff ada turing" {
		t.Fatalf("expected discarded staged handoff to restore draft, got %q", m.room.ComposeValue())
	}
	if m.room.HasStagedBatch() || m.room.IsComposerStaged() {
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
	if !m.room.HasStagedBatch() {
		t.Fatal("expected staged handoff before ada becomes idle")
	}

	inviteParticipant(t, s, "cat")
	m = pumpUntilAgentsStarted(t, m, "cat")
	if err := s.Execute(session.PrivateSendCommand{Alias: "cat", Text: "busy"}); err != nil {
		t.Fatalf("make cat busy: %v", err)
	}

	ada.push(agent.Message{StreamID: testTurnAnchor, Mode: agent.ModeFlush, Content: agent.Output{}})
	m = pumpUntil(t, m, isHandoff("ada", "turing"))

	if hasRecord(m, record.KindSystem, `error: handoff "ada" -> "turing"`) {
		t.Fatalf("did not expect busy late joiner to block staged handoff; records: %v", m.room.HistoryRecords())
	}
	if !hasRecord(m, record.KindSystem, "[handoff ada -> turing]") {
		t.Fatalf("expected handoff record after dispatch; records: %v", m.room.HistoryRecords())
	}
}
