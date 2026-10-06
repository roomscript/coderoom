package agent_test

import (
	"errors"
	"testing"

	"github.com/roomscript/coderoom/internal/agent"
)

// stubAgent is a minimal Agent whose Send returns a configurable anchor and
// whose Read delivers a pre-loaded message queue. Used to unit-test SendAndWait.
type stubAgent struct {
	anchor   agent.StreamID
	sendErr  error
	messages []agent.Message
	pos      int
}

func (s *stubAgent) Start() error                              { return nil }
func (s *stubAgent) Stop() error                               { return nil }
func (s *stubAgent) Interrupt() error                          { return nil }
func (s *stubAgent) Send(string) (agent.StreamID, error)       { return s.anchor, s.sendErr }
func (s *stubAgent) SendNotice(string) (agent.StreamID, error) { return "", nil }
func (s *stubAgent) Read() (agent.Message, error) {
	if s.pos >= len(s.messages) {
		return agent.Message{}, errors.New("no more messages")
	}
	m := s.messages[s.pos]
	s.pos++
	return m, nil
}

// TestSendAndWait_anchorIsAuthoritativeTurnEnd verifies that SendAndWait
// returns when the anchor flush arrives, not when individual content streams
// close.
//
// This is the regression test for issue 3 (premature idle) at the SendAndWait
// level: without the anchor check, SendAndWait would return as soon as the last
// observed output stream closed, even if the turn was not yet complete from the
// adapter's perspective. With the anchor, it returns exactly once — on the
// anchor flush — regardless of how many output streams were emitted.
//
// Message sequence mirrors what turn/completed produces for Codex:
//
//	Output+ModeStream (item 1 delta)
//	Output+ModeFlush  (item 1 close)    ← without anchor: would return here
//	Output+ModeStream (item 2 delta)
//	Output+ModeFlush  (item 2 close)    ← without anchor: seenOutput && open==0
//	Output+ModeFlush  (anchor)          ← with anchor: definitive return
func TestSendAndWait_anchorIsAuthoritativeTurnEnd(t *testing.T) {
	const anchorID = agent.StreamID("codex:active-turn")

	a := &stubAgent{
		anchor: anchorID,
		messages: []agent.Message{
			{StreamID: "out1", Mode: agent.ModeStream, Content: agent.Output{Text: "hello "}},
			{StreamID: "out1", Mode: agent.ModeFlush, Content: agent.Output{}},
			{StreamID: "out2", Mode: agent.ModeStream, Content: agent.Output{Text: "world"}},
			{StreamID: "out2", Mode: agent.ModeFlush, Content: agent.Output{}},
			// Anchor flush — the only stream whose close drives return.
			{StreamID: anchorID, Mode: agent.ModeFlush, Content: agent.Output{}},
		},
	}

	got, err := agent.SendAndWait(a, "prompt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "hello world" {
		t.Fatalf("expected %q, got %q", "hello world", got)
	}
	// All 5 messages consumed; no early return.
	if a.pos != len(a.messages) {
		t.Fatalf("expected all %d messages consumed, consumed %d", len(a.messages), a.pos)
	}
}

// TestSendAndWait_noAnchorFallsBackToHeuristic verifies graceful degradation
// for adapters that return an empty anchor from Send. SendAndWait must still
// terminate using the seenOutput && allClosed heuristic.
func TestSendAndWait_noAnchorFallsBackToHeuristic(t *testing.T) {
	a := &stubAgent{
		anchor: "", // no anchor
		messages: []agent.Message{
			{StreamID: "out1", Mode: agent.ModeStream, Content: agent.Output{Text: "ok"}},
			{StreamID: "out1", Mode: agent.ModeFlush, Content: agent.Output{}},
		},
	}

	got, err := agent.SendAndWait(a, "prompt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "ok" {
		t.Fatalf("expected %q, got %q", "ok", got)
	}
}

// TestSendAndWait_anchorHandlesPureReasoningTurn verifies that SendAndWait
// returns correctly for a turn that produces no visible output text — only
// reasoning — when an anchor is provided.
//
// Without the anchor, SendAndWait would never return (seenOutput=false,
// len(open)==0 is never satisfied via the heuristic since no Output+ModeStream
// was observed). With the anchor, the anchor flush is the unconditional exit.
func TestSendAndWait_anchorHandlesPureReasoningTurn(t *testing.T) {
	const anchorID = agent.StreamID("codex:active-turn")

	a := &stubAgent{
		anchor: anchorID,
		messages: []agent.Message{
			// No output deltas — only reasoning and the anchor.
			{StreamID: "reason1", Mode: agent.ModeStream, Content: agent.Reasoning{Text: "hmm"}},
			{StreamID: "reason1", Mode: agent.ModeFlush, Content: agent.Reasoning{}},
			{StreamID: anchorID, Mode: agent.ModeFlush, Content: agent.Output{}},
		},
	}

	got, err := agent.SendAndWait(a, "prompt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "" {
		t.Fatalf("expected empty output for pure-reasoning turn, got %q", got)
	}
}
