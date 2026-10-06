// Command codex-record records live Codex transcript fixtures from prompt.md
// cases under internal/agent/codex/testdata/transcripts.
package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/roomscript/coderoom/internal/agent"
	"github.com/roomscript/coderoom/internal/agent/codex"
	"github.com/roomscript/coderoom/internal/transcript"
)

const transcriptRoot = "internal/agent/codex/testdata/transcripts"

type transcriptCase struct {
	version string
	name    string
	dir     string
}

type collector struct {
	turnAnchor agent.StreamID
	workDir    string

	outputCount      int
	outputText       strings.Builder
	logCount         int
	logText          strings.Builder
	reasoningCount   int
	reasoningText    strings.Builder
	reasoningStreams map[agent.StreamID]toolStreamState
	fileChangeCount  int
	commandCount     int

	filePaths         []string
	commands          []string
	approvals         []transcript.ApprovalExpectation
	noticeTurnFlushes int
}

const noticeTurnStreamID = agent.StreamID("codex:notice-turn")

func main() {
	cases, err := resolveSelection(transcriptRoot, os.Args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "resolve fixture: %v\n", err)
		os.Exit(1)
	}

	for _, tc := range cases {
		if _, err := fmt.Fprintf(os.Stdout, "recording transcript %s (%s)\n", tc.name, tc.version); err != nil {
			fmt.Fprintf(os.Stderr, "write progress: %v\n", err)
			os.Exit(1)
		}
		input, readErr := transcript.ReadInputDir(tc.dir)
		if readErr != nil {
			fmt.Fprintf(os.Stderr, "read scenario %s (%s): %v\n", tc.name, tc.dir, readErr)
			os.Exit(1)
		}

		if err := recordCase(tc.dir, tc.version, tc.name, input); err != nil {
			fmt.Fprintf(os.Stderr, "record transcript %s (%s): %v\n", tc.name, tc.dir, err)
			os.Exit(1)
		}
		if _, err := fmt.Fprintf(os.Stdout, "recorded transcript %s (%s)\n", tc.name, tc.version); err != nil {
			fmt.Fprintf(os.Stderr, "write progress: %v\n", err)
			os.Exit(1)
		}
	}
}

func recordCase(caseDir, version, testCase string, input transcript.Input) error {
	workDir, err := os.MkdirTemp("", "codex-record-*")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer cleanupDir(workDir)

	restoreEnv, err := setCodexVersion(version)
	if err != nil {
		return err
	}
	defer restoreEnv()

	recorder := transcript.NewObserver()
	collector := &collector{workDir: workDir}
	client, err := startClient(workDir, testCase, input.Config, recorder, collector)
	if err != nil {
		return err
	}
	defer stopClient(client)

	if err := runScenario(client, input.Actions, collector); err != nil {
		return err
	}
	fixture := buildFixture(version, testCase, input, collector)
	return writeTranscript(filepath.Join(caseDir, "output.transcript"), fixture, recorder.Steps())
}

func startClient(workDir, _ string, input transcript.Config, recorder *transcript.Observer, collector *collector) (*codex.Client, error) {
	listener := approvalListenerFunc(func(req agent.ApprovalRequest) agent.ApprovalDecision {
		choice := chooseApproval(req.Options)
		collector.approvals = append(collector.approvals, transcript.ApprovalExpectation{
			Kind:     req.Kind,
			Decision: choice,
		})
		return agent.ApprovalDecision{Choice: choice}
	})
	opts := []codex.Option{
		codex.WithObserver(recorder),
		codex.WithApprovalListener(listener),
		codex.WithModel(input.Model),
		codex.WithSystemPrompt(input.DeveloperInstructions),
		codex.WithAskForApprovalPolicy(input.AskForApproval),
		codex.WithSandboxMode(input.Sandbox),
		codex.WithReasoningEffort(input.ReasoningEffort),
		codex.WithReasoningSummary(input.ReasoningSummary),
	}
	client := codex.New(workDir, opts...)
	if err := client.Start(); err != nil {
		return nil, fmt.Errorf("start client: %w", err)
	}
	return client, nil
}

func runScenario(client *codex.Client, actions []transcript.Action, collector *collector) error {
	for _, action := range actions {
		if err := runAction(client, action, collector); err != nil {
			return err
		}
	}
	return nil
}

func runAction(client *codex.Client, action transcript.Action, collector *collector) error {
	anchor, err := sendAction(client, action)
	if err != nil {
		return err
	}
	collector.turnAnchor = anchor
	for {
		msg, err := client.Read()
		if err != nil {
			return fmt.Errorf("read message: %w", err)
		}
		collector.observe(msg)
		if msg.StreamID == anchor && msg.Mode == agent.ModeFlush {
			return nil
		}
	}
}

func sendAction(client *codex.Client, action transcript.Action) (agent.StreamID, error) {
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

func buildFixture(version, testCase string, input transcript.Input, collector *collector) transcript.Output {
	var noticeExpect *transcript.NoticeExpectation
	if scenarioHasNotice(input.Actions) {
		noticeExpect = &transcript.NoticeExpectation{
			NumTurnFlushes: collector.noticeTurnFlushes,
		}
	}
	return transcript.Output{
		Name:                  testCase,
		CodexVersion:          version,
		Model:                 input.Config.Model,
		DeveloperInstructions: input.Config.DeveloperInstructions,
		Actions:               input.Actions,
		Expect: transcript.Expect{
			Output: transcript.TextExpectation{NumMessages: collector.outputCount, Content: collector.outputText.String()},
			Log:    transcript.TextExpectation{NumMessages: collector.logCount, Content: collector.logText.String()},
			Reasoning: transcript.ReasoningExpectation{
				NumMessages: collector.reasoningCount,
				Content:     collector.reasoningText.String(),
				NumStreams:  collector.reasoningNumStreams(),
				AllFlushed:  collector.reasoningAllFlushed(),
			},
			FileChange: transcript.FileChangeExpectation{
				NumMessages: collector.fileChangeCount,
				Files:       collector.filePaths,
			},
			Command: transcript.CommandExpectation{
				NumMessages: collector.commandCount,
				Executed:    collector.commands,
			},
			Notice:    noticeExpect,
			Approvals: collector.approvals,
		},
	}
}

func scenarioHasNotice(actions []transcript.Action) bool {
	for _, action := range actions {
		if action.Kind == "notice" {
			return true
		}
	}
	return false
}

func (c *collector) observe(msg agent.Message) {
	if c.observeLifecycleFlush(msg) {
		return
	}
	switch content := msg.Content.(type) {
	case agent.Output:
		c.observeOutput(msg, content)
	case agent.Log:
		c.observeLog(msg, content)
	case agent.Reasoning:
		c.observeReasoning(msg, content)
	case agent.FileChangeSet:
		c.observeFileChange(content)
	case agent.Command:
		c.observeCommand(content)
	}
}

func (c *collector) observeOutput(msg agent.Message, content agent.Output) {
	if msg.StreamID == c.turnAnchor && msg.Mode == agent.ModeFlush {
		return
	}
	c.outputCount++
	c.outputText.WriteString(content.Text)
}

func (c *collector) observeLog(msg agent.Message, content agent.Log) {
	if codex.IsStderrLogStream(msg.StreamID) {
		return
	}
	c.logCount++
	c.logText.WriteString(content.Text)
}

func (c *collector) observeReasoning(msg agent.Message, content agent.Reasoning) {
	c.reasoningCount++
	c.reasoningText.WriteString(content.Text)
	c.observeReasoningStream(msg)
}

func (c *collector) observeFileChange(content agent.FileChangeSet) {
	c.fileChangeCount++
	for _, change := range content.Changes {
		appendUnique(&c.filePaths, c.normalizeRecordedPath(change.Path))
	}
}

func (c *collector) observeCommand(content agent.Command) {
	c.commandCount++
	if strings.TrimSpace(content.Command) != "" {
		appendUnique(&c.commands, content.Command)
	}
}

func (c *collector) observeLifecycleFlush(msg agent.Message) bool {
	if msg.StreamID == noticeTurnStreamID && msg.Mode == agent.ModeFlush {
		c.noticeTurnFlushes++
		return true
	}
	if msg.StreamID == c.turnAnchor && msg.Mode == agent.ModeFlush {
		return true
	}
	return false
}

func (c *collector) observeReasoningStream(msg agent.Message) {
	if c.reasoningStreams == nil {
		c.reasoningStreams = make(map[agent.StreamID]toolStreamState)
	}
	state := c.reasoningStreams[msg.StreamID]
	if msg.Mode == agent.ModeStream {
		state.sawStream = true
	}
	if msg.Mode == agent.ModeFlush {
		state.sawFlush = true
	}
	c.reasoningStreams[msg.StreamID] = state
}

func (c *collector) normalizeRecordedPath(path string) string {
	if c.workDir == "" || !filepath.IsAbs(path) {
		return path
	}
	rel, err := filepath.Rel(c.workDir, path)
	if err != nil {
		return path
	}
	if rel == "." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." {
		return path
	}
	return filepath.Clean(rel)
}

func (c *collector) reasoningNumStreams() int {
	count := 0
	for _, state := range c.reasoningStreams {
		if state.sawStream {
			count++
		}
	}
	return count
}

func (c *collector) reasoningAllFlushed() bool {
	for _, state := range c.reasoningStreams {
		if state.sawStream && !state.sawFlush {
			return false
		}
	}
	return true
}

type approvalListenerFunc func(req agent.ApprovalRequest) agent.ApprovalDecision

type toolStreamState struct {
	sawStream bool
	sawFlush  bool
}

func (f approvalListenerFunc) Decide(_ context.Context, req agent.ApprovalRequest) (agent.ApprovalDecision, error) {
	return f(req), nil
}

func chooseApproval(options []agent.ApprovalOption) agent.ApprovalOption {
	preferred := []agent.ApprovalOption{
		agent.OptionAccept,
		agent.OptionAcceptForSession,
	}
	for _, choice := range preferred {
		if slices.Contains(options, choice) {
			return choice
		}
	}
	if len(options) > 0 {
		return options[0]
	}
	return agent.OptionDecline
}

func writeTranscript(path string, output transcript.Output, steps []transcript.Step) error {
	finalPath := filepath.Clean(path)
	var buf bytes.Buffer
	if err := transcript.WriteOutput(&buf, output, steps); err != nil {
		return fmt.Errorf("encode transcript: %w", err)
	}
	tmpPath := filepath.Clean(finalPath + ".tmp")
	if err := os.WriteFile(tmpPath, buf.Bytes(), 0o600); err != nil {
		return fmt.Errorf("write temp transcript: %w", err)
	}
	if err := os.Rename(tmpPath, finalPath); err != nil {
		return fmt.Errorf("rename transcript: %w", err)
	}
	return nil
}

func setCodexVersion(version string) (func(), error) {
	const key = "CODEX_VERSION_OVERRIDE"
	old, hadOld := os.LookupEnv(key)
	if err := os.Setenv(key, version); err != nil {
		return nil, fmt.Errorf("set codex version override: %w", err)
	}
	return func() {
		if hadOld {
			if err := os.Setenv(key, old); err != nil {
				fmt.Fprintf(os.Stderr, "restore %s: %v\n", key, err)
			}
			return
		}
		if err := os.Unsetenv(key); err != nil {
			fmt.Fprintf(os.Stderr, "unset %s: %v\n", key, err)
		}
	}, nil
}

func appendUnique(dst *[]string, value string) {
	value = strings.TrimSpace(value)
	if value == "" || slices.Contains(*dst, value) {
		return
	}
	*dst = append(*dst, value)
}

func resolveSelection(root string, args []string) ([]transcriptCase, error) {
	if len(args) == 0 {
		return resolveAllCases(root)
	}
	if len(args) == 1 {
		return resolveVersionCases(root, args[0])
	}
	if len(args) == 2 {
		version, testCase := args[0], args[1]
		return []transcriptCase{{
			version: version,
			name:    testCase,
			dir:     filepath.Join(root, version, testCase),
		}}, nil
	}
	return nil, errors.New("usage: codex-record [<codex-version> [<test-case>]]")
}

func listDirNames(root string) ([]string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("read directory %q: %w", root, err)
	}
	var names []string
	for _, entry := range entries {
		if entry.IsDir() {
			names = append(names, entry.Name())
		}
	}
	slices.Sort(names)
	return names, nil
}

func resolveAllCases(root string) ([]transcriptCase, error) {
	versions, err := listDirNames(root)
	if err != nil {
		return nil, err
	}
	var cases []transcriptCase
	for _, version := range versions {
		versionCases, err := resolveVersionCases(root, version)
		if err != nil {
			return nil, err
		}
		cases = append(cases, versionCases...)
	}
	return cases, nil
}

func resolveVersionCases(root, version string) ([]transcriptCase, error) {
	names, err := listDirNames(filepath.Join(root, version))
	if err != nil {
		return nil, err
	}
	cases := make([]transcriptCase, 0, len(names))
	for _, name := range names {
		cases = append(cases, transcriptCase{
			version: version,
			name:    name,
			dir:     filepath.Join(root, version, name),
		})
	}
	return cases, nil
}

func cleanupDir(path string) {
	if err := os.RemoveAll(path); err != nil {
		fmt.Fprintf(os.Stderr, "remove temp dir %q: %v\n", path, err)
	}
}

func stopClient(client *codex.Client) {
	if err := client.Stop(); err != nil {
		fmt.Fprintf(os.Stderr, "stop client: %v\n", err)
	}
}
