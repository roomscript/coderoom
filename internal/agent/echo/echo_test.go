package echo_test

import (
	"testing"

	"github.com/roomscript/coderoom/internal/agent"
	"github.com/roomscript/coderoom/internal/agent/echo"
)

func TestClientEchoesPrompt(t *testing.T) {
	c := echo.New()
	if err := c.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = c.Stop() })

	got, err := agent.SendAndWait(c, "repeat this exactly")
	if err != nil {
		t.Fatalf("SendAndWait: %v", err)
	}
	if got != "repeat this exactly" {
		t.Fatalf("output = %q, want %q", got, "repeat this exactly")
	}
}

func TestClientNoticeProducesOnlyCompletion(t *testing.T) {
	c := echo.New()
	if err := c.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = c.Stop() })

	anchor, err := c.SendNotice("context")
	if err != nil {
		t.Fatalf("SendNotice: %v", err)
	}
	msg, err := c.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if msg.StreamID != anchor || msg.Mode != agent.ModeFlush {
		t.Fatalf("completion = %#v, want anchor flush", msg)
	}
}
