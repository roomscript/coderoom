// Command claude-record records live Claude transcript fixtures from prompt.md
// cases under internal/agent/claude/testdata/transcripts.
package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/roomscript/coderoom/internal/agent"
	"github.com/roomscript/coderoom/internal/agent/claude"
	"github.com/roomscript/coderoom/internal/transcript"
)

const transcriptRoot = "internal/agent/claude/testdata/transcripts"

type transcriptCase struct {
	version string
	name    string
	dir     string
}

const noticeTurnStreamID = agent.StreamID("claude:notice-turn")

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
	if _, err := recordingOptions(input.Config); err != nil {
		return err
	}
	if err := validateActions(input.Actions); err != nil {
		return err
	}
	if err := checkInstalledVersion(version); err != nil {
		return err
	}
	workDir, err := os.MkdirTemp("", "claude-record-*")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer cleanupDir(workDir)

	recorder := transcript.NewObserver(nil)
	activity := transcript.NewTurnActivityObserver(recorder, nil)
	collector := transcript.NewCollector(transcript.CollectorOptions{
		NoticeAnchor: noticeTurnStreamID, IgnoreLog: claude.IsStderrLogStream,
		NormalizePath: func(path string) string { return normalizeRecordedPath(workDir, path) },
	})
	var approvals []transcript.ApprovalExpectation
	client, err := startClient(workDir, input.Config, activity, &approvals)
	if err != nil {
		return err
	}
	defer stopClient(client)

	if err := transcript.RunActions(client, input.Actions, collector, activity); err != nil {
		return fmt.Errorf("run scenario: %w", err)
	}
	if err := recorder.Err(); err != nil {
		return fmt.Errorf("record protocol: %w", err)
	}
	fixture := buildFixture(version, testCase, input, collector, approvals)
	return writeRecording(caseDir, fixture, recorder.Steps())
}

func writeRecording(caseDir string, fixture transcript.Output, steps []transcript.Step) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("read home directory: %w", err)
	}
	fixture, steps, err = redactRecording(fixture, steps, home)
	if err != nil {
		return err
	}
	return writeTranscript(filepath.Join(caseDir, "output.transcript"), fixture, steps)
}

func startClient(workDir string, input transcript.Config, observer claude.ProtocolObserver, approvals *[]transcript.ApprovalExpectation) (*claude.Client, error) {
	listener := approvalListenerFunc(func(req agent.ApprovalRequest) agent.ApprovalDecision {
		choice := agent.OptionAccept
		if input.Options["approval"] == "decline" {
			choice = agent.OptionDecline
		}
		if !slices.Contains(req.Options, choice) {
			choice = agent.OptionDecline
		}
		*approvals = append(*approvals, transcript.ApprovalExpectation{
			Kind:     req.Kind,
			Decision: choice,
		})
		return agent.ApprovalDecision{Choice: choice}
	})
	opts, err := recordingOptions(input)
	if err != nil {
		return nil, err
	}
	opts = append(opts, claude.WithObserver(observer), claude.WithApprovalListener(listener))
	client := claude.New(workDir, opts...)
	if err := client.Start(); err != nil {
		return nil, fmt.Errorf("start client: %w", err)
	}
	return client, nil
}

func buildFixture(version, testCase string, input transcript.Input, collector *transcript.Collector, approvals []transcript.ApprovalExpectation) transcript.Output {
	return transcript.Output{
		Name:           testCase,
		Backend:        "claude",
		BackendVersion: version,
		Effort:         input.Config.Effort,
		Options:        input.Config.Options,
		Model:          input.Config.Model,
		SystemPrompt:   input.Config.SystemPrompt,
		Actions:        input.Actions,
		Expect:         collector.Expectations(input.Actions, approvals),
	}
}

func normalizeRecordedPath(workDir, path string) string {
	if workDir == "" || !filepath.IsAbs(path) {
		return path
	}
	rel, err := filepath.Rel(workDir, path)
	if err != nil {
		return path
	}
	if rel == "." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." {
		return path
	}
	return filepath.Clean(rel)
}

type approvalListenerFunc func(req agent.ApprovalRequest) agent.ApprovalDecision

func (f approvalListenerFunc) Decide(_ context.Context, req agent.ApprovalRequest) (agent.ApprovalDecision, error) {
	return f(req), nil
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

func readInstalledVersion() (string, error) {
	raw, err := exec.Command("claude", "--version").Output()
	if err != nil {
		return "", fmt.Errorf("read Claude version: %w", err)
	}
	fields := strings.Fields(string(raw))
	if len(fields) == 0 {
		return "", errors.New("empty Claude version")
	}
	return fields[0], nil
}

func checkInstalledVersion(want string) error {
	version, err := readInstalledVersion()
	if err != nil {
		return err
	}
	if version != want {
		return fmt.Errorf("installed Claude version %q does not match directory %q", version, want)
	}
	return nil
}

func resolveSelection(root string, args []string) ([]transcriptCase, error) {
	if len(args) == 0 {
		version, err := readInstalledVersion()
		if err != nil {
			return nil, err
		}
		return resolveVersionCases(root, version)
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
	return nil, errors.New("usage: claude-record [<claude-version> [<test-case>]]")
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

func stopClient(client *claude.Client) {
	if err := client.Stop(); err != nil {
		fmt.Fprintf(os.Stderr, "stop client: %v\n", err)
	}
}
