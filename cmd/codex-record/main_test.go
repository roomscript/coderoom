package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/roomscript/coderoom/internal/agent"
	"go.uber.org/goleak"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

func TestCollectorNormalizeRecordedPath(t *testing.T) {
	c := &collector{workDir: "/tmp/codex-record-123"}

	if got := c.normalizeRecordedPath("/tmp/codex-record-123/file.txt"); got != "file.txt" {
		t.Fatalf("normalizeRecordedPath(file) = %q, want file.txt", got)
	}
	if got := c.normalizeRecordedPath("/tmp/codex-record-123/nested/file.txt"); got != filepath.Join("nested", "file.txt") {
		t.Fatalf("normalizeRecordedPath(nested) = %q", got)
	}
	other := "/tmp/other/file.txt"
	if got := c.normalizeRecordedPath(other); got != other {
		t.Fatalf("normalizeRecordedPath(other) = %q, want %q", got, other)
	}
	relative := "already-relative.txt"
	if got := c.normalizeRecordedPath(relative); got != relative {
		t.Fatalf("normalizeRecordedPath(relative) = %q, want %q", got, relative)
	}
}

func TestResolveSelection(t *testing.T) {
	root := filepath.Join("..", "..", "internal", "agent", "codex", "testdata", "transcripts")

	cases, err := resolveSelection(root, []string{"0.133.0", "approvals-file-change"})
	if err != nil {
		t.Fatalf("resolveSelection: %v", err)
	}
	if len(cases) != 1 {
		t.Fatalf("cases = %d, want 1", len(cases))
	}
	if cases[0].version != "0.133.0" || cases[0].name != "approvals-file-change" {
		t.Fatalf("case = %#v, want version=%q name=%q", cases[0], "0.133.0", "approvals-file-change")
	}
}

func TestResolveSelection_AllCases(t *testing.T) {
	root := t.TempDir()
	mustMakeCase(t, root, "0.133.0", "alpha")
	mustMakeCase(t, root, "0.133.0", "beta")
	mustMakeCase(t, root, "0.200.0", "gamma")

	cases, err := resolveSelection(root, nil)
	if err != nil {
		t.Fatalf("resolveSelection: %v", err)
	}
	if len(cases) != 3 {
		t.Fatalf("cases = %d, want 3", len(cases))
	}
	if cases[0].version != "0.133.0" || cases[0].name != "alpha" {
		t.Fatalf("first case = %#v", cases[0])
	}
	if cases[2].version != "0.200.0" || cases[2].name != "gamma" {
		t.Fatalf("last case = %#v", cases[2])
	}
}

func TestResolveSelection_VersionCases(t *testing.T) {
	root := t.TempDir()
	mustMakeCase(t, root, "0.133.0", "alpha")
	mustMakeCase(t, root, "0.133.0", "beta")
	mustMakeCase(t, root, "0.200.0", "gamma")

	cases, err := resolveSelection(root, []string{"0.133.0"})
	if err != nil {
		t.Fatalf("resolveSelection: %v", err)
	}
	if len(cases) != 2 {
		t.Fatalf("cases = %d, want 2", len(cases))
	}
	if cases[0].version != "0.133.0" || cases[0].name != "alpha" {
		t.Fatalf("first case = %#v", cases[0])
	}
	if cases[1].version != "0.133.0" || cases[1].name != "beta" {
		t.Fatalf("second case = %#v", cases[1])
	}
}

func TestCollectorObserve_ignoresStderrLogs(t *testing.T) {
	var c collector
	c.observe(agent.Message{
		StreamID: agent.StreamID("codex:stderr"),
		Mode:     agent.ModeSingle,
		Content:  agent.Log{Text: "stderr noise"},
	})
	if c.logCount != 0 || c.logText.String() != "" {
		t.Fatalf("stderr log should be ignored, got count=%d content=%q", c.logCount, c.logText.String())
	}
}

func mustMakeCase(t *testing.T, root, version, name string) {
	t.Helper()
	caseDir := filepath.Join(root, version, name)
	if err := os.MkdirAll(caseDir, 0o750); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
}
