//go:build integration

package codex_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/roomscript/coderoom/internal/agent"
	"github.com/roomscript/coderoom/internal/agent/codex"
	"github.com/roomscript/coderoom/internal/transcript"
)

const transcriptRoot = "testdata/transcripts"

// TestClientLiveTranscriptScenarios is a shared-session smoke test. It starts
// one live Codex client, then reuses the recorded transcript scenario actions
// as convenient real-world prompts and notices to exercise an ongoing session.
// The goal is to catch adapter or protocol breakage while driving a realistic
// conversation sequence, not to validate each scenario's startup configuration
// in isolation. Scenario-level thread/start settings such as startup
// instructions are therefore out of scope for this test.
func TestClientLiveTranscriptScenarios(t *testing.T) {
	versionDir := latestTranscriptVersionDir(t, transcriptRoot)
	cases := discoverTranscriptCases(t, versionDir)
	if len(cases) == 0 {
		t.Fatal("no transcript cases found")
	}

	workDir := t.TempDir()

	listener := approvalListenerFunc(func(_ context.Context, req agent.ApprovalRequest) (agent.ApprovalDecision, error) {
		return agent.ApprovalDecision{Choice: chooseLiveApproval(req.Options)}, nil
	})
	client := codex.New(
		workDir,
		codex.WithObserver(wireObserverForTest(t)),
		codex.WithApprovalListener(listener),
		codex.WithAskForApprovalPolicy(codex.AskOnRequest),
		codex.WithSandboxMode(codex.SandboxDangerFull),
		codex.WithReasoningEffort(codex.ReasoningMedium),
		codex.WithReasoningSummary(codex.ReasoningSummaryDetailed),
	)
	startClient(t, client)

	stats := &liveTranscriptStats{}
	for _, tc := range cases {
		stats.scenariosRun++
		if err := runLiveTranscriptCase(client, tc.input, stats); err != nil {
			t.Fatalf("run %s: %v", tc.name, err)
		}
	}

	assertLiveTranscriptStats(t, cases, stats)
}

type liveTranscriptCase struct {
	name  string
	input transcript.Input
}

type liveTranscriptStats struct {
	scenariosRun int

	outputMessages     int
	commandMessages    int
	fileChangeMessages int
	reasoningMessages  int
	noticeTurnFlushes  int
}

func runLiveTranscriptCase(client *codex.Client, input transcript.Input, stats *liveTranscriptStats) error {
	for _, action := range input.Actions {
		anchor, err := sendLiveAction(client, action)
		if err != nil {
			return err
		}
		if err := drainLiveAction(client, anchor, stats); err != nil {
			return err
		}
	}
	return nil
}

func sendLiveAction(client *codex.Client, action transcript.Action) (agent.StreamID, error) {
	switch action.Kind {
	case "", "prompt":
		anchor, err := client.Send(action.Text)
		if err != nil {
			return "", fmt.Errorf("send prompt: %w", err)
		}
		return anchor, nil
	case "notice":
		anchor, err := client.SendNotice(action.Text)
		if err != nil {
			return "", fmt.Errorf("send notice: %w", err)
		}
		return anchor, nil
	default:
		return "", fmt.Errorf("unknown action kind %q", action.Kind)
	}
}

func drainLiveAction(client *codex.Client, anchor agent.StreamID, stats *liveTranscriptStats) error {
	for {
		msg, err := client.Read()
		if err != nil {
			return fmt.Errorf("read message: %w", err)
		}
		observeLiveMessage(msg, anchor, stats)
		if msg.StreamID == anchor && msg.Mode == agent.ModeFlush {
			return nil
		}
	}
}

func observeLiveMessage(msg agent.Message, anchor agent.StreamID, stats *liveTranscriptStats) {
	if msg.StreamID == agent.StreamID("codex:notice-turn") && msg.Mode == agent.ModeFlush {
		stats.noticeTurnFlushes++
		return
	}
	if msg.StreamID == anchor && msg.Mode == agent.ModeFlush {
		return
	}

	switch msg.Content.(type) {
	case agent.Output:
		stats.outputMessages++
	case agent.Command:
		stats.commandMessages++
	case agent.FileChangeSet:
		stats.fileChangeMessages++
	case agent.Reasoning:
		stats.reasoningMessages++
	}
}

func assertLiveTranscriptStats(t *testing.T, cases []liveTranscriptCase, stats *liveTranscriptStats) {
	t.Helper()

	noticeCases := 0
	for _, tc := range cases {
		if inputHasActionKind(tc.input, "notice") {
			noticeCases++
		}
	}

	if stats.scenariosRun != len(cases) {
		t.Fatalf("scenarios_run = %d, want %d", stats.scenariosRun, len(cases))
	}
	if stats.outputMessages == 0 {
		t.Fatal("expected at least one output message across transcript scenarios")
	}
	if stats.commandMessages == 0 {
		t.Fatal("expected at least one command message across transcript scenarios")
	}
	if stats.fileChangeMessages == 0 {
		t.Fatal("expected at least one file-change message across transcript scenarios")
	}
	if stats.reasoningMessages == 0 {
		t.Fatal("expected at least one reasoning message across transcript scenarios")
	}
	if noticeCases > 0 && stats.noticeTurnFlushes == 0 {
		t.Fatal("expected at least one notice turn flush across transcript scenarios")
	}
}

func inputHasActionKind(input transcript.Input, kind string) bool {
	for _, action := range input.Actions {
		if action.Kind == kind {
			return true
		}
	}
	return false
}

func chooseLiveApproval(options []agent.ApprovalOption) agent.ApprovalOption {
	preferred := []agent.ApprovalOption{
		agent.OptionAccept,
		agent.OptionAcceptForSession,
	}
	for _, choice := range preferred {
		if slices.Contains(options, choice) {
			return choice
		}
	}
	if len(options) == 0 {
		return agent.OptionDecline
	}
	return options[0]
}

func discoverTranscriptCases(t *testing.T, versionDir string) []liveTranscriptCase {
	t.Helper()

	entries, err := os.ReadDir(versionDir)
	if err != nil {
		t.Fatalf("ReadDir(%q): %v", versionDir, err)
	}
	var names []string
	for _, entry := range entries {
		if entry.IsDir() {
			names = append(names, entry.Name())
		}
	}
	slices.Sort(names)

	cases := make([]liveTranscriptCase, 0, len(names))
	for _, name := range names {
		caseDir := filepath.Join(versionDir, name)
		input, err := transcript.ReadInputDir(caseDir)
		if err != nil {
			t.Fatalf("ReadInputDir(%q): %v", caseDir, err)
		}
		cases = append(cases, liveTranscriptCase{
			name:  name,
			input: input,
		})
	}
	return cases
}

func latestTranscriptVersionDir(t *testing.T, root string) string {
	t.Helper()

	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("ReadDir(%q): %v", root, err)
	}

	var (
		bestName string
		best     []int
	)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		version, ok := parseTranscriptVersion(entry.Name())
		if !ok {
			continue
		}
		if best == nil || compareTranscriptVersion(version, best) > 0 {
			bestName = entry.Name()
			best = version
		}
	}
	if bestName == "" {
		t.Fatalf("no transcript version directories found under %q", root)
	}
	return filepath.Join(root, bestName)
}

func parseTranscriptVersion(raw string) ([]int, bool) {
	parts := strings.Split(raw, ".")
	if len(parts) == 0 {
		return nil, false
	}
	version := make([]int, 0, len(parts))
	for _, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil {
			return nil, false
		}
		version = append(version, n)
	}
	return version, true
}

func compareTranscriptVersion(left, right []int) int {
	limit := len(left)
	if len(right) > limit {
		limit = len(right)
	}
	for i := 0; i < limit; i++ {
		lv, rv := 0, 0
		if i < len(left) {
			lv = left[i]
		}
		if i < len(right) {
			rv = right[i]
		}
		switch {
		case lv < rv:
			return -1
		case lv > rv:
			return 1
		}
	}
	return 0
}
