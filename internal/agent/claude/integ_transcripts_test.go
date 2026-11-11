//go:build integration

package claude_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/roomscript/coderoom/internal/agent"
	"github.com/roomscript/coderoom/internal/agent/claude"
	"github.com/roomscript/coderoom/internal/transcript"
)

// TestClientLiveTranscriptScenarios checks session health and broad drift from
// the newest fixtures. Strict tool and notice contracts belong to integ_live_test.go.
func TestClientLiveTranscriptScenarios(t *testing.T) {
	baseline := latestClaudeTranscriptVersion(t, "testdata/transcripts")
	raw, err := exec.Command("claude", "--version").Output()
	if err != nil {
		t.Fatalf("read installed Claude version (baseline %s): %v", baseline, err)
	}
	installed := strings.TrimSpace(string(raw))
	versions := fmt.Sprintf("baseline %s; installed %s", baseline, installed)
	t.Log(versions)
	paths := findTranscriptFixtures(t, filepath.Join("testdata/transcripts", baseline))
	if len(paths) == 0 {
		t.Fatal("no transcript fixtures found")
	}

	var shared *claude.Client
	sharedFailed := false
	sharedListener := &fixedListener{}
	for _, path := range paths {
		name := filepath.Base(filepath.Dir(path))
		fixture, err := readTranscriptFixture(path)
		if err != nil {
			t.Fatalf("case %s (baseline %s; installed %s): %v", name, baseline, installed, err)
		}
		useShared := sharesLiveTranscriptSession(fixture)
		if useShared && !sharedFailed && shared == nil {
			shared, _, _ = startLiveClient(t, claude.WithModel(fixture.Model), claude.WithSystemPrompt(fixture.SystemPrompt), claude.WithApprovalListener(sharedListener))
		}
		if !t.Run(name, func(t *testing.T) {
			runLiveTranscriptSubtest(t, shared, sharedListener, fixture, useShared, versions)
		}) && useShared {
			sharedFailed = true
			shared = nil
		}
	}
}

func runLiveTranscriptSubtest(t *testing.T, shared *claude.Client, sharedListener *fixedListener, fixture transcript.Output, useShared bool, versions string) {
	t.Helper()
	if useShared && shared == nil {
		t.Skip("shared session state is unknown after an earlier failure")
	}
	validateLiveTranscript(t, fixture)
	session := "fresh"
	if useShared {
		session = "shared"
	}
	t.Logf("case %s; session %s; %s", t.Name(), session, versions)
	client, listener := shared, sharedListener
	if !useShared {
		listener = &fixedListener{}
		client, _, _ = startLiveClient(t, claude.WithModel(fixture.Model), claude.WithSystemPrompt(fixture.SystemPrompt), claude.WithApprovalListener(listener))
	}
	runLiveTranscriptScenario(t, client, listener, fixture, useShared, versions)
}

func validateLiveTranscript(t *testing.T, fixture transcript.Output) {
	t.Helper()
	if fixture.Effort != "" {
		t.Fatal("Claude does not support header effort")
	}
	for key, value := range fixture.Options {
		if key != "approval" || (value != "accept" && value != "decline") {
			t.Fatalf("unsupported Claude option %s=%q", key, value)
		}
	}
	for _, action := range transcript.NormalizedActions(fixture) {
		if action.Kind == "model-interrupt" {
			t.Fatal("Claude does not support model-interrupt")
		}
	}
}

func sharesLiveTranscriptSession(fixture transcript.Output) bool {
	if fixture.Model != "haiku" || fixture.SystemPrompt != "" {
		return false
	}
	for _, action := range transcript.NormalizedActions(fixture) {
		if transcript.IsModelAction(action.Kind) {
			return false
		}
	}
	return true
}

func runLiveTranscriptScenario(t *testing.T, client *claude.Client, listener *fixedListener, fixture transcript.Output, shared bool, versions string) {
	t.Helper()
	choice := agent.OptionAccept
	if fixture.Options["approval"] == "decline" {
		choice = agent.OptionDecline
	}
	listener.mu.Lock()
	listener.choice, listener.reqs = choice, nil
	listener.mu.Unlock()
	collector := transcript.NewCollector(transcript.CollectorOptions{
		NoticeAnchor: agent.StreamID("claude:notice-turn"), IgnoreLog: claude.IsStderrLogStream,
	})
	actions := transcript.NormalizedActions(fixture)
	activity := transcript.NewTurnActivityObserver(nil, nil)
	runLiveTranscriptActions(t, client, actions, collector, activity, versions)
	var approvals []transcript.ApprovalExpectation
	for _, req := range listener.requests() {
		approvals = append(approvals, transcript.ApprovalExpectation{Kind: req.Kind, Decision: choice})
	}
	current := transcript.Output{Expect: collector.Expectations(actions, approvals)}
	if shared {
		fixture.Expect.Reasoning = transcript.ReasoningExpectation{}
		current.Expect.Reasoning = transcript.ReasoningExpectation{}
	}
	if err := transcript.CompareUpgradeOutputs(fixture, current); err != nil {
		t.Fatalf("live scenario drift for %s (%s): %v", t.Name(), versions, err)
	}
}

func runLiveTranscriptActions(t *testing.T, client *claude.Client, actions []transcript.Action, collector *transcript.Collector, activity *transcript.TurnActivityObserver, versions string) {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- transcript.RunActions(client, actions, collector, activity) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run actions for %s (%s): %v", t.Name(), versions, err)
		}
	case <-time.After(time.Duration(max(1, len(actions))) * liveTurnTimeout):
		t.Fatalf("scenario %s (%s) did not finish within %s per action", t.Name(), versions, liveTurnTimeout)
	}
}

func latestClaudeTranscriptVersion(t *testing.T, root string) string {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	var best string
	var bestParts []int
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		parts, ok := parseClaudeTranscriptVersion(entry.Name())
		if ok && (best == "" || slices.Compare(parts, bestParts) > 0) {
			best, bestParts = entry.Name(), parts
		}
	}
	if best == "" {
		t.Fatalf("no transcript version directories under %s", root)
	}
	return best
}

func parseClaudeTranscriptVersion(version string) ([]int, bool) {
	parts := strings.Split(version, ".")
	if len(parts) != 3 {
		return nil, false
	}
	numbers := make([]int, len(parts))
	for i, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 {
			return nil, false
		}
		numbers[i] = n
	}
	return numbers, true
}
