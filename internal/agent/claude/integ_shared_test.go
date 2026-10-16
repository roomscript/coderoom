//go:build integration

package claude_test

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/roomscript/coderoom/internal/agent"
	"github.com/roomscript/coderoom/internal/agent/claude"
)

var (
	logDir      = flag.String("logdir", "", "directory to write Claude wire logs into (if empty, uses the test temp dir)")
	claudeModel = flag.String("claude-model", "haiku", "model for live Claude tests")
)

const liveTurnTimeout = 3 * time.Minute

// wireObserver writes every line to a log file and keeps the latest
// system/init line for assertions.
type wireObserver struct {
	mu   sync.Mutex
	w    io.Writer
	init json.RawMessage
}

func (o *wireObserver) OnSend(msg string) { o.write("→", msg) }

func (o *wireObserver) OnReceive(msg string) {
	o.write("←", msg)
	var env struct{ Type, Subtype string }
	if json.Unmarshal([]byte(msg), &env) != nil || env.Type != "system" || env.Subtype != "init" {
		return
	}
	o.mu.Lock()
	o.init = json.RawMessage(msg)
	o.mu.Unlock()
}

func (o *wireObserver) write(dir, msg string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	_, _ = fmt.Fprintf(o.w, "%s %s %s\n", time.Now().UTC().Format(time.RFC3339Nano), dir, msg)
}

func (o *wireObserver) initLine() json.RawMessage {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.init
}

func newWireObserver(t *testing.T) *wireObserver {
	t.Helper()
	dir := *logDir
	if dir == "" {
		dir = t.TempDir()
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("logdir mkdir: %v", err)
	}
	f, err := os.CreateTemp(dir, "claude-wire-*.log")
	if err != nil {
		t.Fatalf("wire log temp: %v", err)
	}
	t.Logf("claude wire log: %s", f.Name())
	t.Cleanup(func() { _ = f.Close() })
	return &wireObserver{w: f}
}

// startLiveClient starts a real Claude Code in a fresh directory. A missing
// claude binary fails the test, as a missing codex does, so a passing run
// never hides tests that did not run.
func startLiveClient(t *testing.T, opts ...claude.Option) (*claude.Client, *wireObserver, string) {
	t.Helper()
	if _, err := exec.LookPath("claude"); err != nil {
		t.Fatal("claude not installed; the Claude integration tests need Claude Code on PATH")
	}
	cwd := t.TempDir()
	obs := newWireObserver(t)
	opts = append([]claude.Option{claude.WithObserver(obs), claude.WithModel(*claudeModel)}, opts...)
	c := claude.New(cwd, opts...)
	if err := c.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		if err := c.Stop(); err != nil {
			t.Errorf("Stop: %v", err)
		}
	})
	return c, obs, cwd
}

// fixedListener answers every approval with one choice and records the
// requests.
type fixedListener struct {
	mu     sync.Mutex
	choice agent.ApprovalOption
	reqs   []agent.ApprovalRequest
}

func (l *fixedListener) Decide(_ context.Context, req agent.ApprovalRequest) (agent.ApprovalDecision, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.reqs = append(l.reqs, req)
	return agent.ApprovalDecision{Choice: l.choice}, nil
}

func (l *fixedListener) requests() []agent.ApprovalRequest {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.reqs)
}

// waitingListener blocks until its request is withdrawn.
type waitingListener struct {
	called   chan struct{}
	released chan struct{}
}

func (l *waitingListener) Decide(ctx context.Context, _ agent.ApprovalRequest) (agent.ApprovalDecision, error) {
	close(l.called)
	<-ctx.Done()
	close(l.released)
	return agent.ApprovalDecision{}, fmt.Errorf("decide: %w", ctx.Err())
}

// turnResult holds one turn's output, each stream accumulated to its final
// message.
type turnResult struct {
	streams map[agent.StreamID]agent.Message
	logs    []string
}

// commands returns the accumulated Command streams.
func (r turnResult) commands() []agent.Command {
	var out []agent.Command
	for _, m := range r.streams {
		if cmd, ok := m.Content.(agent.Command); ok {
			out = append(out, cmd)
		}
	}
	return out
}

// fileChanges returns the accumulated FileChangeSet streams.
func (r turnResult) fileChanges() []agent.FileChangeSet {
	var out []agent.FileChangeSet
	for _, m := range r.streams {
		if set, ok := m.Content.(agent.FileChangeSet); ok {
			out = append(out, set)
		}
	}
	return out
}

// readTurn reads until the anchor flush. On timeout it fails the test; the
// cleanup Stop then unblocks the reader goroutine.
func readTurn(t *testing.T, c *claude.Client, anchor agent.StreamID) turnResult {
	t.Helper()
	done := make(chan error, 1)
	res := turnResult{streams: map[agent.StreamID]agent.Message{}}
	go func() { done <- collectTurn(c, anchor, &res) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("read turn: %v", err)
		}
		return res
	case <-time.After(liveTurnTimeout):
		t.Fatalf("turn did not end within %s", liveTurnTimeout)
		return turnResult{}
	}
}

func collectTurn(c *claude.Client, anchor agent.StreamID, res *turnResult) error {
	for {
		m, err := c.Read()
		if err != nil {
			return fmt.Errorf("read: %w", err)
		}
		if m.StreamID == anchor && m.Mode == agent.ModeFlush {
			return nil
		}
		if l, ok := m.Content.(agent.Log); ok {
			res.logs = append(res.logs, l.Text)
			continue
		}
		prev, ok := res.streams[m.StreamID]
		if !ok {
			res.streams[m.StreamID] = m
			continue
		}
		acc, err := prev.Accumulate(m)
		if err != nil {
			return fmt.Errorf("accumulate %s: %w", m.StreamID, err)
		}
		res.streams[m.StreamID] = acc
	}
}
