package codex_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/roomscript/coderoom/internal/agent"
	"github.com/roomscript/coderoom/internal/agent/codex"
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

	repoRoot := mustRepoRoot(t)
	listener := newReplayApprovalListener(file.Expect.Approvals)
	command, args := replayCommand(t, path)
	client := codex.New(
		repoRoot,
		codex.WithAppServerCommand(command, args...),
		codex.WithApprovalListener(listener),
		codex.WithModel(file.Model),
		codex.WithSystemPrompt(file.DeveloperInstructions),
	)
	startReplayClient(t, client)

	collector := &replayCollector{}
	for _, action := range transcript.NormalizedActions(file) {
		anchor, err := replaySendAction(client, action)
		if err != nil {
			t.Fatalf("send action: %v", err)
		}
		collector.turnAnchor = anchor

		for {
			msg, err := client.Read()
			if err != nil {
				t.Fatalf("Read: %v", err)
			}
			collector.observe(msg)
			if msg.StreamID == anchor && msg.Mode == agent.ModeFlush {
				break
			}
		}
	}

	if err := assertReplayExpectations(file.Expect, collector, listener); err != nil {
		t.Fatalf("assert replay expectations: %v", err)
	}
}

func replaySendAction(client *codex.Client, action transcript.Action) (agent.StreamID, error) {
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
		filepath.Join(repoRoot, "cmd", "codex-replay"),
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

type replayCollector struct {
	turnAnchor agent.StreamID

	outputCount     int
	outputText      strings.Builder
	logCount        int
	logText         strings.Builder
	reasoningCount  int
	reasoningText   strings.Builder
	fileChangeCount int
	commandCount    int

	filePaths []string
	commands  []string

	commandStreams    map[agent.StreamID]commandStreamState
	fileChangeStreams map[agent.StreamID]toolStreamState
	reasoningStreams  map[agent.StreamID]toolStreamState
	noticeTurnFlushes int
	errs              []error
}

func (c *replayCollector) observe(msg agent.Message) {
	if c.observeLifecycleFlush(msg) {
		return
	}
	switch content := msg.Content.(type) {
	case agent.Output:
		c.observeOutput(msg, content)
	case agent.Log:
		c.observeLog(content)
	case agent.Reasoning:
		c.observeReasoning(msg, content)
	case agent.FileChangeSet:
		c.observeFileChange(msg, content)
	case agent.Command:
		c.observeCommand(msg, content)
	}
}

func (c *replayCollector) observeOutput(msg agent.Message, content agent.Output) {
	if msg.StreamID == c.turnAnchor && msg.Mode == agent.ModeFlush {
		return
	}
	c.outputCount++
	c.outputText.WriteString(content.Text)
}

func (c *replayCollector) observeLog(content agent.Log) {
	c.logCount++
	c.logText.WriteString(content.Text)
}

func (c *replayCollector) observeReasoning(msg agent.Message, content agent.Reasoning) {
	c.reasoningCount++
	c.reasoningText.WriteString(content.Text)
	c.observeReasoningStream(msg, content)
}

func (c *replayCollector) observeFileChange(msg agent.Message, content agent.FileChangeSet) {
	c.fileChangeCount++
	for _, change := range content.Changes {
		replayAppendUnique(&c.filePaths, change.Path)
	}
	c.observeFileChangeStream(msg, content)
}

func (c *replayCollector) observeCommand(msg agent.Message, content agent.Command) {
	c.commandCount++
	if strings.TrimSpace(content.Command) != "" {
		replayAppendUnique(&c.commands, content.Command)
	}
	c.observeCommandStream(msg, content)
}

func (c *replayCollector) observeLifecycleFlush(msg agent.Message) bool {
	if msg.StreamID == agent.StreamID("codex:notice-turn") && msg.Mode == agent.ModeFlush {
		c.noticeTurnFlushes++
		return true
	}
	if msg.StreamID == c.turnAnchor && msg.Mode == agent.ModeFlush {
		return true
	}
	return false
}

func normalizeReplayFilePath(path string) string {
	if !filepath.IsAbs(path) {
		return path
	}
	parts := strings.Split(filepath.Clean(path), string(filepath.Separator))
	for i, part := range parts {
		if !strings.HasPrefix(part, "codex-record-") {
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

type commandStreamState struct {
	sawStart    bool
	sawExitCode bool
	sawFlush    bool
}

type toolStreamState struct {
	sawStream bool
	sawFlush  bool
}

func (c *replayCollector) observeCommandStream(msg agent.Message, content agent.Command) {
	if c.commandStreams == nil {
		c.commandStreams = make(map[agent.StreamID]commandStreamState)
	}
	state := c.commandStreams[msg.StreamID]
	if msg.Mode == agent.ModeStream && content.Command != "" {
		state.sawStart = true
	}
	if msg.Mode == agent.ModeStream && content.ExitCode != nil {
		state.sawExitCode = true
	}
	if msg.Mode == agent.ModeFlush {
		state.sawFlush = true
		if content.Command != "" || content.Output != "" || content.ExitCode != nil {
			c.errs = append(c.errs, fmt.Errorf("command flush for %q carried non-zero content %+v", msg.StreamID, content))
		}
	}
	c.commandStreams[msg.StreamID] = state
}

func (c *replayCollector) observeFileChangeStream(msg agent.Message, content agent.FileChangeSet) {
	if c.fileChangeStreams == nil {
		c.fileChangeStreams = make(map[agent.StreamID]toolStreamState)
	}
	state := c.fileChangeStreams[msg.StreamID]
	if msg.Mode == agent.ModeStream {
		state.sawStream = true
	}
	if msg.Mode == agent.ModeFlush {
		state.sawFlush = true
		if content.Status != "" || len(content.Changes) != 0 {
			c.errs = append(c.errs, fmt.Errorf("file change flush for %q carried non-zero content %+v", msg.StreamID, content))
		}
	}
	c.fileChangeStreams[msg.StreamID] = state
}

func (c *replayCollector) observeReasoningStream(msg agent.Message, content agent.Reasoning) {
	if c.reasoningStreams == nil {
		c.reasoningStreams = make(map[agent.StreamID]toolStreamState)
	}
	state := c.reasoningStreams[msg.StreamID]
	if msg.Mode == agent.ModeStream {
		state.sawStream = true
		if content.Text == "" {
			c.errs = append(c.errs, fmt.Errorf("reasoning stream for %q carried empty text", msg.StreamID))
		}
	}
	if msg.Mode == agent.ModeFlush {
		state.sawFlush = true
		if content.Text != "" {
			c.errs = append(c.errs, fmt.Errorf("reasoning flush for %q carried non-zero content %+v", msg.StreamID, content))
		}
	}
	c.reasoningStreams[msg.StreamID] = state
}

func replayAppendUnique(dst *[]string, value string) {
	value = strings.TrimSpace(value)
	if value == "" || slices.Contains(*dst, value) {
		return
	}
	*dst = append(*dst, value)
}

type replayApprovalListener struct {
	expected []transcript.ApprovalExpectation
	observed []transcript.ApprovalExpectation
	err      error
}

func newReplayApprovalListener(expected []transcript.ApprovalExpectation) *replayApprovalListener {
	return &replayApprovalListener{expected: expected}
}

func (l *replayApprovalListener) Decide(_ context.Context, req agent.ApprovalRequest) (agent.ApprovalDecision, error) {
	choice := agent.OptionDecline
	index := len(l.observed)
	if index >= len(l.expected) {
		if l.err == nil {
			l.err = fmt.Errorf("unexpected approval kind %q", req.Kind)
		}
	} else {
		expected := l.expected[index]
		choice = expected.Decision
		if expected.Kind != req.Kind && l.err == nil {
			l.err = fmt.Errorf("approval %d kind = %q, want %q", index, req.Kind, expected.Kind)
		}
		if !slices.Contains(req.Options, choice) && l.err == nil {
			l.err = fmt.Errorf("approval %d decision %q not allowed by options %v", index, choice, req.Options)
		}
	}
	l.observed = append(l.observed, transcript.ApprovalExpectation{
		Kind:     req.Kind,
		Decision: choice,
	})
	return agent.ApprovalDecision{Choice: choice}, nil
}

func assertReplayExpectations(expect transcript.Expect, collector *replayCollector, listener *replayApprovalListener) error {
	if err := assertReplayPreconditions(collector, listener); err != nil {
		return err
	}
	if err := assertReplayMessageExpectations(expect, collector); err != nil {
		return err
	}
	if err := assertReplayStreamExpectations(expect, collector); err != nil {
		return err
	}
	return nil
}

func assertReplayPreconditions(collector *replayCollector, listener *replayApprovalListener) error {
	if listener.err != nil {
		return listener.err
	}
	if err := assertReplayCollectorErrors(collector.errs); err != nil {
		return err
	}
	if err := assertReplayApprovals(listener.expected, listener.observed); err != nil {
		return err
	}
	return nil
}

func assertReplayMessageExpectations(expect transcript.Expect, collector *replayCollector) error {
	if err := assertReplayTextExpectation("output", expect.Output, collector.outputCount, collector.outputText.String()); err != nil {
		return err
	}
	if err := assertReplayTextExpectation("log", expect.Log, collector.logCount, collector.logText.String()); err != nil {
		return err
	}
	if err := assertReplayReasoningExpectation(expect.Reasoning, collector); err != nil {
		return err
	}
	if err := assertReplayFileChangeExpectation(expect.FileChange, collector); err != nil {
		return err
	}
	if err := assertReplayCommandExpectation(expect.Command, collector); err != nil {
		return err
	}
	if err := assertReplayNoticeExpectation(expect.Notice, collector.noticeTurnFlushes); err != nil {
		return err
	}
	return nil
}

func assertReplayStreamExpectations(expect transcript.Expect, collector *replayCollector) error {
	if err := assertReplayCommandStreams(expect.Command, collector.commandStreams); err != nil {
		return err
	}
	if err := assertReplayFileChangeStreams(expect.FileChange, collector.fileChangeStreams); err != nil {
		return err
	}
	return nil
}

func assertReplayNoticeExpectation(expected *transcript.NoticeExpectation, got int) error {
	if expected == nil {
		return nil
	}
	if got != expected.NumTurnFlushes {
		return fmt.Errorf("notice.num_turn_flushes = %d, want %d", got, expected.NumTurnFlushes)
	}
	return nil
}

func assertReplayCollectorErrors(errs []error) error {
	if len(errs) == 0 {
		return nil
	}
	return errs[0]
}

func assertReplayApprovals(expected, observed []transcript.ApprovalExpectation) error {
	if len(observed) != len(expected) {
		return fmt.Errorf("approval count = %d, want %d", len(observed), len(expected))
	}
	if !reflect.DeepEqual(observed, expected) {
		return fmt.Errorf("approvals = %v, want %v", observed, expected)
	}
	return nil
}

func assertReplayTextExpectation(label string, expected transcript.TextExpectation, count int, content string) error {
	if count != expected.NumMessages {
		return fmt.Errorf("%s.num_messages = %d, want %d", label, count, expected.NumMessages)
	}
	if content != expected.Content {
		return fmt.Errorf("%s.content = %q, want %q", label, content, expected.Content)
	}
	return nil
}

func assertReplayReasoningExpectation(expected transcript.ReasoningExpectation, collector *replayCollector) error {
	if collector.reasoningCount != expected.NumMessages {
		return fmt.Errorf("reasoning.num_messages = %d, want %d", collector.reasoningCount, expected.NumMessages)
	}
	if collector.reasoningText.String() != expected.Content {
		return fmt.Errorf("reasoning.content = %q, want %q", collector.reasoningText.String(), expected.Content)
	}
	numStreams, allFlushed := replayReasoningSummary(collector.reasoningStreams)
	if numStreams != expected.NumStreams {
		return fmt.Errorf("reasoning.num_streams = %d, want %d", numStreams, expected.NumStreams)
	}
	if allFlushed != expected.AllFlushed {
		return fmt.Errorf("reasoning.all_flushed = %t, want %t", allFlushed, expected.AllFlushed)
	}
	return nil
}

func replayReasoningSummary(streams map[agent.StreamID]toolStreamState) (int, bool) {
	numStreams := 0
	allFlushed := true
	for _, state := range streams {
		if state.sawStream {
			numStreams++
			if !state.sawFlush {
				allFlushed = false
			}
		}
	}
	return numStreams, allFlushed
}

func assertReplayFileChangeExpectation(expected transcript.FileChangeExpectation, collector *replayCollector) error {
	if collector.fileChangeCount != expected.NumMessages {
		return fmt.Errorf("file_change.num_messages = %d, want %d", collector.fileChangeCount, expected.NumMessages)
	}
	if !reflect.DeepEqual(collector.filePaths, expected.Files) {
		normalized := make([]string, 0, len(collector.filePaths))
		for _, path := range collector.filePaths {
			normalized = append(normalized, normalizeReplayFilePath(path))
		}
		if reflect.DeepEqual(normalized, expected.Files) {
			return nil
		}
		return fmt.Errorf("file_change.files = %v, want %v", collector.filePaths, expected.Files)
	}
	return nil
}

func assertReplayCommandExpectation(expected transcript.CommandExpectation, collector *replayCollector) error {
	if collector.commandCount != expected.NumMessages {
		return fmt.Errorf("command.num_messages = %d, want %d", collector.commandCount, expected.NumMessages)
	}
	if !reflect.DeepEqual(collector.commands, expected.Executed) {
		return fmt.Errorf("command.executed = %v, want %v", collector.commands, expected.Executed)
	}
	return nil
}

func assertReplayCommandStreams(expected transcript.CommandExpectation, streams map[agent.StreamID]commandStreamState) error {
	if expected.NumMessages == 0 {
		return nil
	}
	if len(streams) == 0 {
		return fmt.Errorf("expected command stream messages, got none")
	}
	for streamID, state := range streams {
		if !state.sawStart {
			return fmt.Errorf("command stream %q missing started message", streamID)
		}
		if !state.sawExitCode {
			return fmt.Errorf("command stream %q missing exit code message", streamID)
		}
		if !state.sawFlush {
			return fmt.Errorf("command stream %q missing flush", streamID)
		}
	}
	return nil
}

func assertReplayFileChangeStreams(expected transcript.FileChangeExpectation, streams map[agent.StreamID]toolStreamState) error {
	if expected.NumMessages == 0 {
		return nil
	}
	if len(streams) == 0 {
		return fmt.Errorf("expected file change stream messages, got none")
	}
	for streamID, state := range streams {
		if !state.sawStream {
			return fmt.Errorf("file change stream %q missing stream message", streamID)
		}
		if !state.sawFlush {
			return fmt.Errorf("file change stream %q missing flush", streamID)
		}
	}
	return nil
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
