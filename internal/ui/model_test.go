package ui

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/roomscript/coderoom/internal/agent"
	"github.com/roomscript/coderoom/internal/interpreter"
	"github.com/roomscript/coderoom/internal/session"
	"github.com/roomscript/coderoom/internal/ui/room/history/record"
)

// --- handleEvent: records ---

func TestHandleEvent_agentStarted(t *testing.T) {
	m := makeReadyModel(t)
	m = pushEvent(m, session.AgentReady{Alias: "ada"})
	if !hasRecord(m, record.KindSystem, "[ada joined]") {
		t.Errorf("expected [ada joined] system record; records: %v", m.room.HistoryRecords())
	}
}

func TestHandleEvent_agentStarting(t *testing.T) {
	m := makeReadyModel(t)
	m = pushEvent(m, session.AgentStarting{Alias: "ada"})
	if !hasRecord(m, record.KindSystem, "[ada starting]") {
		t.Errorf("expected [ada starting] system record; records: %v", m.room.HistoryRecords())
	}
}

func TestHandleEvent_agentStopped(t *testing.T) {
	m := makeReadyModel(t)
	m = pushEvent(m, session.AgentReady{Alias: "ada"})
	m = pushEvent(m, session.AgentStopped{Alias: "ada"})
	if !hasRecord(m, record.KindSystem, "[ada left]") {
		t.Errorf("expected [ada left] system record; records: %v", m.room.HistoryRecords())
	}
}

func TestHandleEvent_agentCrashed(t *testing.T) {
	m := makeReadyModel(t)
	m = pushEvent(m, session.AgentReady{Alias: "ada"})
	m = pushEvent(m, session.AgentCrashed{Alias: "ada"})
	if !hasRecord(m, record.KindSystem, "[ada crashed]") {
		t.Errorf("expected [ada crashed] system record; records: %v", m.room.HistoryRecords())
	}
}

func TestHandleEvent_agentLog(t *testing.T) {
	m := makeReadyModel(t)
	m = pushEvent(m, session.AgentLog{Alias: "ada", Text: "npm warn something"})
	if !hasRecord(m, record.KindLog, "npm warn something") {
		t.Errorf("expected log record with text; records: %v", m.room.HistoryRecords())
	}
}

func TestHandleEvent_sharedNoticeProducesNoSystemRecord(t *testing.T) {
	m := makeReadyModel(t)
	m = pushEvent(m, session.RoutingCompleted{Result: session.RoutingResult{Kind: session.RoutingParticipantSend}})
	if len(m.room.HistoryRecords()) != 0 {
		t.Errorf("expected no history record for shared notice; got %v", m.room.HistoryRecords())
	}
}

func TestHandleEvent_broadcastAndSharedSendProduceNoSystemRecord(t *testing.T) {
	events := []session.Event{
		session.RoutingCompleted{Result: session.RoutingResult{Kind: session.RoutingBroadcast}},
		session.RoutingCompleted{Result: session.RoutingResult{Kind: session.RoutingParticipantSend}},
	}
	for _, e := range events {
		m := makeReadyModel(t)
		m = pushEvent(m, e)
		if len(m.room.HistoryRecords()) != 0 {
			t.Errorf("expected no system record for %T; got %v", e, m.room.HistoryRecords())
		}
	}
}

func TestTranscript_contextHandoffPresentedOnce(t *testing.T) {
	m := makeReadyModel(t)
	m = pushEvent(m, session.HandoffDelivered{FromAlias: "ada",
		ToAlias: "turing",
		Text:    "final answer",
		Preview: "[handoff ada -> turing]\n  ↦ source: ada latest output\n  > final answer",
	})
	if !hasRecord(m, record.KindSystem, "[handoff ada -> turing]") {
		t.Fatal("missing canonical handoff audit")
	}
}

// --- streaming ---

func TestHandleDelta_firstDeltaCreatesRecord(t *testing.T) {
	m := makeReadyModel(t)
	m = pushEvent(m, session.AgentMessage{Alias: "ada", Msg: agent.Message{
		StreamID: "out1", Mode: agent.ModeStream, Content: agent.Output{Text: "hello"},
	}})
	recs := m.room.HistoryRecords()
	if len(recs) != 1 {
		t.Fatalf("expected 1 record, got %d", len(recs))
	}
	idx, ok := m.room.StreamingIdx("ada")
	if !ok {
		t.Fatal("expected ada to be streaming")
	}
	rec := recs[idx]
	if rec.Kind != record.KindAgentOutput {
		t.Errorf("expected agent output record, got kind %d", rec.Kind)
	}
	if rec.Alias != "ada" {
		t.Errorf("expected alias ada, got %q", rec.Alias)
	}
	if rec.Msg == nil {
		t.Fatal("expected record to carry Msg, got nil")
	}
	out, ok := rec.Msg.Content.(agent.Output)
	if !ok {
		t.Fatalf("expected Output content, got %T", rec.Msg.Content)
	}
	if out.Text != "hello" {
		t.Errorf("expected body 'hello', got %q", out.Text)
	}
}

func TestHandleDelta_subsequentDeltaAppendsInPlace(t *testing.T) {
	m := makeReadyModel(t)
	m = pushEvent(m, session.AgentMessage{Alias: "ada", Msg: agent.Message{
		StreamID: "out1", Mode: agent.ModeStream, Content: agent.Output{Text: "hello"},
	}})
	m = pushEvent(m, session.AgentMessage{Alias: "ada", Msg: agent.Message{
		StreamID: "out1", Mode: agent.ModeStream, Content: agent.Output{Text: " world"},
	}})
	recs := m.room.HistoryRecords()
	if len(recs) != 1 {
		t.Fatalf("expected 1 record (in-place append), got %d", len(recs))
	}
	idx, _ := m.room.StreamingIdx("ada")
	if recs[idx].Msg == nil {
		t.Fatal("expected record to carry Msg, got nil")
	}
	out, ok := recs[idx].Msg.Content.(agent.Output)
	if !ok {
		t.Fatalf("expected Output content, got %T", recs[idx].Msg.Content)
	}
	if out.Text != "hello world" {
		t.Errorf("expected body 'hello world', got %q", out.Text)
	}
}

func TestHandleDelta_distinctOutputStreamsCreateDistinctRecords(t *testing.T) {
	m := makeReadyModel(t)
	m = pushEvent(m, session.AgentMessage{Alias: "ada", Msg: agent.Message{
		StreamID: "out1", Mode: agent.ModeStream, Content: agent.Output{Text: "hello"},
	}})
	m = pushEvent(m, session.AgentMessage{Alias: "ada", Msg: agent.Message{
		StreamID: "out2", Mode: agent.ModeStream, Content: agent.Output{Text: "world"},
	}})

	recs := m.room.HistoryRecords()
	if len(recs) != 2 {
		t.Fatalf("expected 2 records for distinct output streams, got %d", len(recs))
	}
	if recs[0].Msg == nil || recs[1].Msg == nil {
		t.Fatal("expected output records to carry Msg payloads")
	}

	first, ok := recs[0].Msg.Content.(agent.Output)
	if !ok {
		t.Fatalf("expected first record Output content, got %T", recs[0].Msg.Content)
	}
	second, ok := recs[1].Msg.Content.(agent.Output)
	if !ok {
		t.Fatalf("expected second record Output content, got %T", recs[1].Msg.Content)
	}
	if first.Text != "hello" || second.Text != "world" {
		t.Fatalf("expected separate output records, got %q and %q", first.Text, second.Text)
	}
}

func TestHandleDelta_twoAgentsStreamConcurrently(t *testing.T) {
	m := makeReadyModel(t)
	m = pushEvent(m, session.AgentMessage{Alias: "ada", Msg: agent.Message{
		StreamID: "out-ada", Mode: agent.ModeStream, Content: agent.Output{Text: "a"},
	}})
	m = pushEvent(m, session.AgentMessage{Alias: "bob", Msg: agent.Message{
		StreamID: "out-bob", Mode: agent.ModeStream, Content: agent.Output{Text: "b"},
	}})
	m = pushEvent(m, session.AgentMessage{Alias: "ada", Msg: agent.Message{
		StreamID: "out-ada", Mode: agent.ModeStream, Content: agent.Output{Text: "2"},
	}})
	recs := m.room.HistoryRecords()
	if len(recs) != 2 {
		t.Fatalf("expected 2 records, got %d", len(recs))
	}
	adaIdx, _ := m.room.StreamingIdx("ada")
	bobIdx, _ := m.room.StreamingIdx("bob")
	if recs[adaIdx].Msg == nil {
		t.Fatal("expected ada record to carry Msg, got nil")
	}
	adaOut, ok := recs[adaIdx].Msg.Content.(agent.Output)
	if !ok {
		t.Fatalf("expected ada Output content, got %T", recs[adaIdx].Msg.Content)
	}
	if adaOut.Text != "a2" {
		t.Errorf("ada body wrong: %q", adaOut.Text)
	}
	if recs[bobIdx].Msg == nil {
		t.Fatal("expected bob record to carry Msg, got nil")
	}
	bobOut, ok := recs[bobIdx].Msg.Content.(agent.Output)
	if !ok {
		t.Fatalf("expected bob Output content, got %T", recs[bobIdx].Msg.Content)
	}
	if bobOut.Text != "b" {
		t.Errorf("bob body wrong: %q", bobOut.Text)
	}
}

func TestHandleEvent_outputFlushClearsMatchingStream(t *testing.T) {
	m := makeReadyModel(t)
	m = pushEvent(m, session.AgentMessage{Alias: "ada", Msg: agent.Message{
		StreamID: "out1", Mode: agent.ModeStream, Content: agent.Output{Text: "hello"},
	}})
	m = pushEvent(m, session.AgentMessage{Alias: "ada", Msg: agent.Message{
		StreamID: "out1", Mode: agent.ModeFlush, Content: agent.Output{},
	}})
	if m.room.IsStreaming("ada") {
		t.Error("expected streaming to be cleared after output flush")
	}
}

func TestHandleEvent_outputFlushClosesOnlyMatchingStream(t *testing.T) {
	m := makeReadyModel(t)
	m = pushEvent(m, session.AgentMessage{Alias: "ada", Msg: agent.Message{
		StreamID: "out1", Mode: agent.ModeStream, Content: agent.Output{Text: "hello"},
	}})
	m = pushEvent(m, session.AgentMessage{Alias: "ada", Msg: agent.Message{
		StreamID: "out2", Mode: agent.ModeStream, Content: agent.Output{Text: "world"},
	}})

	if !m.room.IsStreaming("ada") {
		t.Fatal("expected ada to be streaming before output flush")
	}

	m = pushEvent(m, session.AgentMessage{Alias: "ada", Msg: agent.Message{
		StreamID: "out1", Mode: agent.ModeFlush, Content: agent.Output{},
	}})
	if !m.room.IsStreaming("ada") {
		t.Error("expected out2 to remain open after out1 flush")
	}
}

func TestHandleEvent_agentStoppedClearsStreaming(t *testing.T) {
	m := makeReadyModel(t)
	m = pushEvent(m, session.AgentMessage{Alias: "ada", Msg: agent.Message{
		StreamID: "out1", Mode: agent.ModeStream, Content: agent.Output{Text: "mid-stream"},
	}})
	m = pushEvent(m, session.AgentStopped{Alias: "ada"})
	if m.room.IsStreaming("ada") {
		t.Error("streaming should be cleared when agent stops mid-turn")
	}
}

// Routing assertions are retained in workflow_stage_test.go and the native
// send/broadcast lifecycle tests in barrier_batch_test.go.
func TestBroadcast_noAgentsPreservesDraft(t *testing.T) {
	m := makeReadyModel(t)
	m = submitThroughInterpreter(t, m, "hello")
	if !hasRecord(m, record.KindSystem, "error:") {
		t.Errorf("expected dispatch failure presentation; records: %v", m.room.HistoryRecords())
	}
	assertHistoryDoesNotContainUserInput(t, m, "hello")
	if got := m.room.ComposeValue(); got != "hello" {
		t.Fatalf("draft = %q, want hello", got)
	}
}

// --- participants / help rendering ---

func TestWho_noAgents(t *testing.T) {
	m := makeReadyModel(t)
	m = submitThroughInterpreter(t, m, "/who")
	m = consumeInterpreterStateChange(t, m)
	if !hasRecord(m, record.KindSystem, "[no agents]") {
		t.Errorf("expected [no agents] system record; records: %v", m.room.HistoryRecords())
	}
}

func TestRenderHelp_includesMetadataAndUIShortcuts(t *testing.T) {
	m := makeReadyModel(t)
	m = m.renderHelp(interpreter.HelpListed{
		Commands: []interpreter.HelpEntry{{Usage: "/example", Description: "run example"}},
		Messages: []interpreter.HelpEntry{{Usage: "@<alias> <text>", Description: "send message"}},
	})
	for _, text := range []string{"/example", "run example", "@<alias> <text>", "Ctrl+O"} {
		if !hasRecord(m, record.KindSystem, text) {
			t.Errorf("help output missing %q; records: %v", text, m.room.HistoryRecords())
		}
	}
}

// --- departed agent colour ---

func TestMarkDeparted_greyRepaintOnStop(t *testing.T) {
	m := makeReadyModel(t)
	m = pushEvent(m, session.AgentMessage{Alias: "ada", Msg: agent.Message{
		StreamID: "out1", Mode: agent.ModeStream, Content: agent.Output{Text: "hello"},
	}})
	m = pushEvent(m, session.AgentMessage{Alias: "ada", Msg: agent.Message{
		StreamID: "out1", Mode: agent.ModeFlush, Content: agent.Output{},
	}})
	m = pushEvent(m, session.AgentStopped{Alias: "ada"})
	if !m.room.IsDeparted("ada") {
		t.Error("expected ada in departed map after stop")
	}
}

func TestMarkDeparted_greyRepaintOnCrash(t *testing.T) {
	m := makeReadyModel(t)
	m = pushEvent(m, session.AgentMessage{Alias: "ada", Msg: agent.Message{
		StreamID: "out1", Mode: agent.ModeStream, Content: agent.Output{Text: "hello"},
	}})
	m = pushEvent(m, session.AgentCrashed{Alias: "ada"})
	if !m.room.IsDeparted("ada") {
		t.Error("expected ada in departed map after crash")
	}
}

// --- reasoning streaming ---

func TestHandleReasoningDelta_firstDeltaCreatesRecord(t *testing.T) {
	m := makeReadyModel(t)
	m = pushEvent(m, session.AgentMessage{Alias: "ada", Msg: agent.Message{
		StreamID: "reason1", Mode: agent.ModeStream, Content: agent.Reasoning{Text: "let me think"},
	}})
	recs := m.room.HistoryRecords()
	if len(recs) != 1 {
		t.Fatalf("expected 1 record, got %d", len(recs))
	}
	if recs[0].Kind != record.KindReasoning {
		t.Errorf("expected reasoning record, got kind %d", recs[0].Kind)
	}
	if recs[0].Msg == nil {
		t.Fatal("expected record to carry Msg, got nil")
	}
	thought, ok := recs[0].Msg.Content.(agent.Reasoning)
	if !ok {
		t.Fatalf("expected Reasoning content, got %T", recs[0].Msg.Content)
	}
	if thought.Text != "let me think" {
		t.Errorf("expected body 'let me think', got %q", thought.Text)
	}
	if !m.room.IsReasoningStreaming("ada") {
		t.Error("expected ada to be reasoning-streaming")
	}
}

func TestHandleReasoningDelta_appendsInPlace(t *testing.T) {
	m := makeReadyModel(t)
	m = pushEvent(m, session.AgentMessage{Alias: "ada", Msg: agent.Message{
		StreamID: "reason1", Mode: agent.ModeStream, Content: agent.Reasoning{Text: "step 1"},
	}})
	m = pushEvent(m, session.AgentMessage{Alias: "ada", Msg: agent.Message{
		StreamID: "reason1", Mode: agent.ModeStream, Content: agent.Reasoning{Text: " step 2"},
	}})
	recs := m.room.HistoryRecords()
	if len(recs) != 1 {
		t.Fatalf("expected 1 record (in-place append), got %d", len(recs))
	}
	if recs[0].Msg == nil {
		t.Fatal("expected record to carry Msg, got nil")
	}
	thought, ok := recs[0].Msg.Content.(agent.Reasoning)
	if !ok {
		t.Fatalf("expected Reasoning content, got %T", recs[0].Msg.Content)
	}
	if thought.Text != "step 1 step 2" {
		t.Errorf("expected body 'step 1 step 2', got %q", thought.Text)
	}
}

func TestHandleReasoningDelta_independentOfOutputStreaming(t *testing.T) {
	m := makeReadyModel(t)
	m = pushEvent(m, session.AgentMessage{Alias: "ada", Msg: agent.Message{
		StreamID: "reason1", Mode: agent.ModeStream, Content: agent.Reasoning{Text: "thinking"},
	}})
	m = pushEvent(m, session.AgentMessage{Alias: "ada", Msg: agent.Message{
		StreamID: "out1", Mode: agent.ModeStream, Content: agent.Output{Text: "responding"},
	}})
	recs := m.room.HistoryRecords()
	if len(recs) != 2 {
		t.Fatalf("expected 2 records (reasoning + output), got %d", len(recs))
	}
	if recs[0].Kind != record.KindReasoning {
		t.Errorf("expected first record to be reasoning, got kind %d", recs[0].Kind)
	}
	if recs[1].Kind != record.KindAgentOutput {
		t.Errorf("expected second record to be agent output, got kind %d", recs[1].Kind)
	}
}

func TestReasoningFlush_clearsReasoningStreaming(t *testing.T) {
	m := makeReadyModel(t)
	m = pushEvent(m, session.AgentMessage{Alias: "ada", Msg: agent.Message{
		StreamID: "reason1", Mode: agent.ModeStream, Content: agent.Reasoning{Text: "thinking"},
	}})
	m = pushEvent(m, session.AgentMessage{Alias: "ada", Msg: agent.Message{
		StreamID: "reason1", Mode: agent.ModeFlush, Content: agent.Reasoning{},
	}})
	if m.room.IsReasoningStreaming("ada") {
		t.Error("expected reasoning streaming to be cleared after reasoning flush")
	}
}

// --- streaming cleanup ---

func TestStreamingCleared_onStop(t *testing.T) {
	m := makeReadyModel(t)
	m = pushEvent(m, session.AgentMessage{Alias: "ada", Msg: agent.Message{
		StreamID: "out1", Mode: agent.ModeStream, Content: agent.Output{Text: "mid-stream"},
	}})
	if !m.room.IsStreaming("ada") {
		t.Fatal("expected ada to be streaming after delta")
	}
	m = pushEvent(m, session.AgentStopped{Alias: "ada"})
	if m.room.IsStreaming("ada") {
		t.Error("streaming should be cleared on agent stop")
	}
}

func TestModelClose_keepsCallerOwnedInterpreterOpen(t *testing.T) {
	m := makeReadyModel(t)
	t.Cleanup(m.interpreterQueue.Close)
	listener := awaitInterpreterEvent(m.interpreterQueue)
	returned := make(chan tea.Msg, 1)
	go func() { returned <- listener() }()
	select {
	case msg := <-returned:
		t.Fatalf("listener returned before Close: %T", msg)
	case <-time.After(20 * time.Millisecond):
	}
	m.Close()
	select {
	case msg := <-returned:
		if msg != nil {
			t.Fatalf("closed listener returned %T, want nil", msg)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not release blocked UI event listener")
	}
	if err := m.interpreter.Submit("/who"); err != nil {
		t.Fatalf("caller-owned interpreter closed: %v", err)
	}
}
