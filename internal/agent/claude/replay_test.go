package claude_test

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/roomscript/coderoom/internal/agent"
	"github.com/roomscript/coderoom/internal/agent/claude"
	"github.com/roomscript/coderoom/internal/transcript"
)

func TestReplayFixtures(t *testing.T) {
	paths := findTranscriptFixtures(t, "testdata/transcripts")
	if len(paths) == 0 {
		t.Fatal("no transcript fixtures found")
	}
	for _, path := range paths {
		path := path
		t.Run(transcriptCaseName(path), func(t *testing.T) {
			runTranscriptCase(t, path)
		})
	}
}

func runTranscriptCase(t *testing.T, path string) {
	t.Helper()

	file, err := readTranscriptFixture(path)
	if err != nil {
		t.Fatalf("read transcript: %v", err)
	}

	for _, action := range transcript.NormalizedActions(file) {
		if action.Kind == "model-interrupt" {
			t.Fatal("Claude does not support model-interrupt")
		}
	}
	repoRoot := mustRepoRoot(t)
	listener := transcript.NewReplayApprovalListener(file.Expect.Approvals)
	command, args := replayCommand(t, path)
	activity := transcript.NewTurnActivityObserver(nil, nil)
	client := claude.New(repoRoot,
		claude.WithCommand(command, args...),
		claude.WithApprovalListener(listener),
		claude.WithModel(file.Model),
		claude.WithSystemPrompt(file.SystemPrompt),
		claude.WithObserver(activity),
	)
	startReplayClient(t, client)

	collector := transcript.NewCollector(transcript.CollectorOptions{
		NoticeAnchor: agent.StreamID("claude:notice-turn"), IgnoreLog: claude.IsStderrLogStream, NormalizePath: normalizeReplayFilePath,
	})
	if err := transcript.RunActions(client, transcript.NormalizedActions(file), collector, activity); err != nil {
		t.Fatal(err)
	}

	if err := transcript.CheckExpectations(file.Expect, collector, listener); err != nil {
		t.Fatalf("assert replay expectations: %v", err)
	}
}

func findTranscriptFixtures(t *testing.T, root string) []string {
	t.Helper()

	var paths []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || d.Name() != "output.transcript" {
			return nil
		}
		paths = append(paths, path)
		return nil
	})
	if err != nil {
		t.Fatalf("walk transcripts: %v", err)
	}
	slices.Sort(paths)
	return paths
}

func transcriptCaseName(path string) string {
	dir := filepath.Dir(path)
	return filepath.ToSlash(strings.TrimPrefix(dir, "testdata/transcripts/"))
}

func readTranscriptFixture(path string) (transcript.Output, error) {
	file, err := os.Open(filepath.Clean(path))
	if err != nil {
		return transcript.Output{}, fmt.Errorf("open %q: %w", path, err)
	}
	defer func() { _ = file.Close() }()

	fixture, _, err := transcript.ReadOutput(file)
	if err != nil {
		return transcript.Output{}, fmt.Errorf("parse %q: %w", path, err)
	}
	return fixture, nil
}

func replayCommand(t *testing.T, path string) (string, []string) {
	t.Helper()

	repoRoot := mustRepoRoot(t)
	transcriptPath, err := filepath.Abs(path)
	if err != nil {
		t.Fatalf("Abs(%q): %v", path, err)
	}
	return "go", []string{
		"run",
		filepath.Join(repoRoot, "cmd", "claude-replay"),
		"--transcript",
		transcriptPath,
	}
}

func mustRepoRoot(t *testing.T) string {
	t.Helper()

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	return filepath.Clean(filepath.Join(wd, "..", "..", ".."))
}

func normalizeReplayFilePath(path string) string {
	if !filepath.IsAbs(path) {
		return path
	}
	parts := strings.Split(filepath.Clean(path), string(filepath.Separator))
	for i, part := range parts {
		if !strings.HasPrefix(part, "claude-record-") {
			continue
		}
		rel := filepath.Join(parts[i+1:]...)
		if rel == "" || rel == "." {
			return path
		}
		return filepath.Clean(rel)
	}
	return path
}

func startReplayClient(t *testing.T, c agent.Agent) {
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
