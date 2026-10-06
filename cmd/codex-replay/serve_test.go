package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/roomscript/coderoom/internal/transcript"
)

func TestServeReplay(t *testing.T) {
	steps := []transcript.Step{
		{
			Kind: "recv",
			Match: map[string]any{
				"method": "initialize",
				"id":     float64(1),
			},
		},
		{
			Kind:    "send",
			Message: map[string]any{"id": float64(1), "result": map[string]any{}},
		},
	}

	var stdout bytes.Buffer
	err := serveReplay(strings.NewReader("{\"method\":\"initialize\",\"id\":1,\"params\":{\"extra\":true}}\n"), &stdout, steps)
	if err != nil {
		t.Fatalf("serveReplay() error = %v", err)
	}
	if got := stdout.String(); got != "{\"id\":1,\"result\":{}}\n" {
		t.Fatalf("stdout = %q, want encoded replay response", got)
	}
}

func TestServeReplayMismatch(t *testing.T) {
	steps := []transcript.Step{
		{
			Kind: "recv",
			Match: map[string]any{
				"method": "initialize",
				"id":     float64(1),
			},
		},
	}

	err := serveReplay(strings.NewReader("{\"method\":\"thread/start\",\"id\":1}\n"), &bytes.Buffer{}, steps)
	if err == nil {
		t.Fatal("serveReplay() error = nil, want mismatch")
	}
}
