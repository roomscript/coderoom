package codex

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/roomscript/coderoom/internal/agent"
)

// TestMessageFromEnvelope_flushContentIsZero encodes the invariant: every
// ModeFlush message produced by the adapter must carry zero-value content.
// Add a row here whenever a new wire event produces a ModeFlush.
func TestMessageFromEnvelope_flushContentIsZero(t *testing.T) {
	cases := []struct {
		name      string
		line      string
		wantFlush agent.MessageContent // zero-value of the expected flush content type
	}{
		{
			"turn/completed",
			`{"method":"turn/completed","params":{"threadId":"th1","turn":{"id":"t1","status":"completed","items":[{"type":"agentMessage","id":"msg1"}]}}}`,
			agent.Output{},
		},
		{
			"item/reasoning/summaryPartAdded",
			`{"method":"item/reasoning/summaryPartAdded","params":{"itemId":"i1","threadId":"th1","turnId":"t1"}}`,
			agent.Reasoning{},
		},
		{
			"item/completed agentMessage",
			`{"method":"item/completed","params":{"turnId":"t1","threadId":"th1","completedAtMs":0,"item":{"type":"agentMessage","id":"msg1","text":"hello"}}}`,
			agent.Output{},
		},
		{
			// item/completed emits ModeStream{ExitCode} then ModeFlush{}; only the flush is checked.
			"item/completed commandExecution",
			`{"method":"item/completed","params":{"turnId":"t1","threadId":"th1","completedAtMs":0,"item":{"type":"commandExecution","id":"cmd1","command":"ls","cwd":"/","status":"completed","commandActions":[]}}}`,
			agent.Command{},
		},
		{
			// item/completed emits ModeStream{Status+Changes} then ModeFlush{}; only the flush is checked.
			"item/completed fileChange",
			`{"method":"item/completed","params":{"turnId":"t1","threadId":"th1","completedAtMs":0,"item":{"type":"fileChange","id":"fc1","status":"completed","changes":[]}}}`,
			agent.FileChangeSet{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertEnvelopeHasZeroFlush(t, tc.line, tc.wantFlush)
		})
	}
}

func TestMessageFromEnvelope_agentDeltaUsesItemScopedOutputStream(t *testing.T) {
	var env rpcEnvelope
	line := `{"method":"item/agentMessage/delta","params":{"itemId":"msg1","turnId":"t1","delta":"hello"}}`
	if err := json.Unmarshal([]byte(line), &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	msgs, err := messageFromEnvelope(env)
	if err != nil {
		t.Fatalf("messageFromEnvelope: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0].StreamID != agent.StreamID("codex:output:t1:msg1") {
		t.Fatalf("expected item-scoped output stream, got %q", msgs[0].StreamID)
	}
}

func TestMessageFromEnvelope_turnCompletedEmitsOnlyAnchorWhenItemsEmpty(t *testing.T) {
	var env rpcEnvelope
	line := `{"method":"turn/completed","params":{"threadId":"th1","turn":{"id":"t1","status":"completed","items":[],"itemsView":"notLoaded"}}}`
	if err := json.Unmarshal([]byte(line), &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	msgs, err := messageFromEnvelope(env)
	if err != nil {
		t.Fatalf("messageFromEnvelope: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 anchor flush, got %d", len(msgs))
	}
	if msgs[0].StreamID != activeTurnStreamID {
		t.Fatalf("expected anchor stream ID %q, got %q", activeTurnStreamID, msgs[0].StreamID)
	}
}

func TestMessageFromEnvelope_turnCompletedWithItemsFlushesAgentMessages(t *testing.T) {
	var env rpcEnvelope
	line := `{"method":"turn/completed","params":{"threadId":"th1","turn":{"id":"t1","status":"completed","items":[{"type":"agentMessage","id":"msg1"},{"type":"reasoning","id":"r1"},{"type":"agentMessage","id":"msg2"}]}}}`
	if err := json.Unmarshal([]byte(line), &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	msgs, err := messageFromEnvelope(env)
	if err != nil {
		t.Fatalf("messageFromEnvelope: %v", err)
	}
	// agentMessage items produce per-item flushes; reasoning is skipped; anchor is last.
	if len(msgs) != 3 {
		t.Fatalf("expected 2 per-item flushes + 1 anchor, got %d", len(msgs))
	}
	if msgs[0].StreamID != agent.StreamID("codex:output:t1:msg1") {
		t.Fatalf("unexpected stream ID: %q", msgs[0].StreamID)
	}
	if msgs[1].StreamID != agent.StreamID("codex:output:t1:msg2") {
		t.Fatalf("unexpected stream ID: %q", msgs[1].StreamID)
	}
	if msgs[2].StreamID != activeTurnStreamID {
		t.Fatalf("expected anchor last, got %q", msgs[2].StreamID)
	}
}

func TestMessageFromItemCompleted_agentMessageEmitsOutputFlush(t *testing.T) {
	var env rpcEnvelope
	wire := `{"method":"item/completed","params":{"turnId":"t1","threadId":"th1","completedAtMs":0,"item":{"type":"agentMessage","id":"msg1","text":"hello"}}}`
	if err := json.Unmarshal([]byte(wire), &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	msgs, err := messageFromEnvelope(env)
	if err != nil {
		t.Fatalf("messageFromEnvelope: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0].StreamID != agent.StreamID("codex:output:t1:msg1") {
		t.Fatalf("unexpected stream ID: %q", msgs[0].StreamID)
	}
	if msgs[0].Mode != agent.ModeFlush {
		t.Fatalf("expected ModeFlush, got %v", msgs[0].Mode)
	}
	if _, ok := msgs[0].Content.(agent.Output); !ok {
		t.Fatalf("expected Output content, got %T", msgs[0].Content)
	}
}

// TestReasoningStreamLifecycle documents the current protocol contract:
//
//   - item/reasoning/summaryPartAdded → ModeFlush (belt-and-suspenders; a no-op
//     when it fires before deltas, silenced via ErrStreamNotTracked)
//   - item/reasoning/summaryTextDelta → ModeStream; opens the stream
//   - item/completed (reasoning)      → ModeFlush; authoritative close
//
// Both summaryPartAdded and item/completed emit a ModeFlush for the same stream.
// Whichever arrives while the stream is open acts as the close; the other is a
// no-op silenced by ErrStreamNotTracked.
func TestReasoningStreamLifecycle(t *testing.T) {
	const itemID = "r1"
	wantStreamID := reasoningStreamID(itemID)

	summaryPartAdded := `{"method":"item/reasoning/summaryPartAdded","params":{"itemId":"r1","threadId":"th1","turnId":"t1"}}`
	itemCompleted := `{"method":"item/completed","params":{"turnId":"t1","threadId":"th1","completedAtMs":0,"item":{"type":"reasoning","id":"r1","summary":[],"content":[]}}}`

	for _, tc := range []struct {
		name string
		wire string
		want int
	}{
		{"summaryPartAdded emits flush", summaryPartAdded, 1},
		{"item/completed emits flush", itemCompleted, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var env rpcEnvelope
			if err := json.Unmarshal([]byte(tc.wire), &env); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			msgs, err := messageFromEnvelope(env)
			if err != nil {
				t.Fatalf("messageFromEnvelope: %v", err)
			}
			if len(msgs) != tc.want {
				t.Fatalf("got %d message(s), want %d: %+v", len(msgs), tc.want, msgs)
			}
			if tc.want == 0 {
				return
			}
			msg := msgs[0]
			if msg.StreamID != wantStreamID {
				t.Fatalf("stream ID = %q, want %q", msg.StreamID, wantStreamID)
			}
			if msg.Mode != agent.ModeFlush {
				t.Fatalf("mode = %v, want ModeFlush", msg.Mode)
			}
			if _, ok := msg.Content.(agent.Reasoning); !ok {
				t.Fatalf("content = %T, want agent.Reasoning", msg.Content)
			}
		})
	}
}

func assertEnvelopeHasZeroFlush(t *testing.T, line string, wantFlush agent.MessageContent) {
	t.Helper()
	var env rpcEnvelope
	if err := json.Unmarshal([]byte(line), &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	msgs, err := messageFromEnvelope(env)
	if err != nil {
		t.Fatalf("messageFromEnvelope: %v", err)
	}
	var sawFlush bool
	for _, msg := range msgs {
		if msg.Mode != agent.ModeFlush {
			continue
		}
		sawFlush = true
		if !reflect.DeepEqual(msg.Content, wantFlush) {
			t.Errorf("ModeFlush content = %T(%+v), want zero-value %T", msg.Content, msg.Content, wantFlush)
		}
	}
	if !sawFlush {
		t.Errorf("no ModeFlush produced; expected flush of type %T", wantFlush)
	}
}
