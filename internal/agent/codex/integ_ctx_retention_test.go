//go:build integration

package codex_test

import (
	"os"
	"strings"
	"testing"

	"github.com/roomscript/coderoom/internal/agent"
	"github.com/roomscript/coderoom/internal/agent/codex"
)

// TestClientLiveContextRetention remains live because it validates real Codex
// semantic memory across turns rather than adapter protocol behavior.
func TestClientLiveContextRetention(t *testing.T) {
	cwd, _ := os.Getwd()
	c := codex.New(cwd, codex.WithObserver(wireObserverForTest(t)))
	startClient(t, c)

	if _, err := agent.SendAndWait(c, "What is 2 + 2?"); err != nil {
		t.Fatalf("first turn: %v", err)
	}

	result, err := agent.SendAndWait(c, "Multiply that result by 3.")
	if err != nil {
		t.Fatalf("second turn: %v", err)
	}
	if !strings.Contains(result, "12") {
		t.Errorf("expected result to contain '12' (context preserved), got: %s", result)
	}
}
