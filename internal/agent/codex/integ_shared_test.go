//go:build integration

package codex_test

import (
	"context"
	"flag"
	"os"
	"testing"

	"github.com/roomscript/coderoom/internal/agent"
	"github.com/roomscript/coderoom/internal/agent/codex"
)

var logDir = flag.String("logdir", "", "directory to write Codex wire logs into (if empty, uses the test temp dir)")

type approvalListenerFunc func(ctx context.Context, req agent.ApprovalRequest) (agent.ApprovalDecision, error)

func (f approvalListenerFunc) Decide(ctx context.Context, req agent.ApprovalRequest) (agent.ApprovalDecision, error) {
	return f(ctx, req)
}

func wireObserverForTest(t *testing.T) codex.ProtocolObserver {
	t.Helper()
	dir := *logDir
	if dir == "" {
		dir = t.TempDir()
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("wirelog-dir mkdir: %v", err)
	}
	f, err := os.CreateTemp(dir, "codex-wire-*.log")
	if err != nil {
		t.Fatalf("wire log temp: %v", err)
	}
	t.Logf("codex wire log: %s", f.Name())
	t.Cleanup(func() { _ = f.Close() })
	return codex.NewLogObserver(f, "codex")
}

func startClient(t *testing.T, c agent.Agent) {
	t.Helper()
	if err := c.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		if err := c.Stop(); err != nil {
			t.Errorf("Stop: %v", err)
		}
	})
}
