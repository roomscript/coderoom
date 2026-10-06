// Package record tests the history record primitives (rendering and
// accumulation).
package record

import (
	"testing"

	"github.com/roomscript/coderoom/internal/agent"
)

func TestAccumulate_updatesMsgAndCachesText(t *testing.T) {
	r := NewAgent("bot", agent.Message{
		StreamID: "out1",
		Mode:     agent.ModeStream,
		Content:  agent.Output{Text: "hel"},
	})
	if r.Text != "hel" {
		t.Fatalf("expected initial Text %q, got %q", "hel", r.Text)
	}

	next, err := r.Accumulate(agent.Message{
		StreamID: "out1",
		Mode:     agent.ModeStream,
		Content:  agent.Output{Text: "lo"},
	})
	if err != nil {
		t.Fatalf("Accumulate: %v", err)
	}
	if next.Text != "hello" {
		t.Fatalf("expected accumulated Text %q, got %q", "hello", next.Text)
	}
	if next.Msg == nil {
		t.Fatal("expected Msg to be non-nil after accumulate")
	}
	out, ok := next.Msg.Content.(agent.Output)
	if !ok {
		t.Fatalf("expected Output content, got %T", next.Msg.Content)
	}
	if out.Text != "hello" {
		t.Fatalf("expected accumulated Output.Text %q, got %q", "hello", out.Text)
	}
}
