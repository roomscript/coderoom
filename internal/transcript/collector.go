package transcript

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/roomscript/coderoom/internal/agent"
)

// CollectorOptions supplies backend-specific stream and path rules.
type CollectorOptions struct {
	NoticeAnchor  agent.StreamID
	IgnoreLog     func(agent.StreamID) bool
	NormalizePath func(string) string
}

// Collector accumulates transcript expectations and checks stream lifecycles.
// A collector belongs to one sequential scenario.
type Collector struct {
	options    CollectorOptions
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

// Observe accumulates a message and records invalid lifecycle payloads.
func (c *Collector) Observe(msg agent.Message) {
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
		c.observeFileChange(msg, content)
	case agent.Command:
		c.observeCommand(msg, content)
	}
}

func (c *Collector) observeOutput(msg agent.Message, content agent.Output) {
	if msg.StreamID == c.turnAnchor && msg.Mode == agent.ModeFlush {
		return
	}
	c.outputCount++
	c.outputText.WriteString(content.Text)
}

func (c *Collector) observeLog(msg agent.Message, content agent.Log) {
	if c.options.IgnoreLog != nil && c.options.IgnoreLog(msg.StreamID) {
		return
	}
	c.logCount++
	c.logText.WriteString(content.Text)
}

func (c *Collector) observeReasoning(msg agent.Message, content agent.Reasoning) {
	c.reasoningCount++
	c.reasoningText.WriteString(content.Text)
	c.observeReasoningStream(msg, content)
}

func (c *Collector) observeFileChange(msg agent.Message, content agent.FileChangeSet) {
	c.fileChangeCount++
	for _, change := range content.Changes {
		replayAppendUnique(&c.filePaths, change.Path)
	}
	c.observeFileChangeStream(msg, content)
}

func (c *Collector) observeCommand(msg agent.Message, content agent.Command) {
	c.commandCount++
	if strings.TrimSpace(content.Command) != "" {
		replayAppendUnique(&c.commands, content.Command)
	}
	c.observeCommandStream(msg, content)
}

func (c *Collector) observeLifecycleFlush(msg agent.Message) bool {
	if msg.StreamID == c.options.NoticeAnchor && msg.Mode == agent.ModeFlush {
		c.noticeTurnFlushes++
		return true
	}
	if msg.StreamID == c.turnAnchor && msg.Mode == agent.ModeFlush {
		return true
	}
	return false
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

func (c *Collector) observeCommandStream(msg agent.Message, content agent.Command) {
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

func (c *Collector) observeFileChangeStream(msg agent.Message, content agent.FileChangeSet) {
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

func (c *Collector) observeReasoningStream(msg agent.Message, content agent.Reasoning) {
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

// ReplayApprovalListener answers approvals using the recorded decisions.
type ReplayApprovalListener struct {
	expected []ApprovalExpectation
	observed []ApprovalExpectation
	err      error
}

// NewReplayApprovalListener creates a listener for the expected approval sequence.
func NewReplayApprovalListener(expected []ApprovalExpectation) *ReplayApprovalListener {
	return &ReplayApprovalListener{expected: expected}
}

// Decide records the request and returns its recorded decision.
func (l *ReplayApprovalListener) Decide(_ context.Context, req agent.ApprovalRequest) (agent.ApprovalDecision, error) {
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
	l.observed = append(l.observed, ApprovalExpectation{
		Kind:     req.Kind,
		Decision: choice,
	})
	return agent.ApprovalDecision{Choice: choice}, nil
}

// CheckExpectations checks recorded messages, approvals and stream lifecycles.
func CheckExpectations(expect Expect, collector *Collector, listener *ReplayApprovalListener) error {
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

func assertReplayPreconditions(collector *Collector, listener *ReplayApprovalListener) error {
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

func assertReplayMessageExpectations(expect Expect, collector *Collector) error {
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

func assertReplayStreamExpectations(expect Expect, collector *Collector) error {
	if err := assertReplayCommandStreams(expect.Command, collector.commandStreams); err != nil {
		return err
	}
	if err := assertReplayFileChangeStreams(expect.FileChange, collector.fileChangeStreams); err != nil {
		return err
	}
	return nil
}

func assertReplayNoticeExpectation(expected *NoticeExpectation, got int) error {
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

func assertReplayApprovals(expected, observed []ApprovalExpectation) error {
	if len(observed) != len(expected) {
		return fmt.Errorf("approval count = %d, want %d", len(observed), len(expected))
	}
	if !reflect.DeepEqual(observed, expected) {
		return fmt.Errorf("approvals = %v, want %v", observed, expected)
	}
	return nil
}

func assertReplayTextExpectation(label string, expected TextExpectation, count int, content string) error {
	if count != expected.NumMessages {
		return fmt.Errorf("%s.num_messages = %d, want %d", label, count, expected.NumMessages)
	}
	if content != expected.Content {
		return fmt.Errorf("%s.content = %q, want %q", label, content, expected.Content)
	}
	return nil
}

func assertReplayReasoningExpectation(expected ReasoningExpectation, collector *Collector) error {
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

func assertReplayFileChangeExpectation(expected FileChangeExpectation, collector *Collector) error {
	if collector.fileChangeCount != expected.NumMessages {
		return fmt.Errorf("file_change.num_messages = %d, want %d", collector.fileChangeCount, expected.NumMessages)
	}
	if !reflect.DeepEqual(collector.filePaths, expected.Files) {
		normalized := make([]string, 0, len(collector.filePaths))
		for _, path := range collector.filePaths {
			if collector.options.NormalizePath != nil {
				path = collector.options.NormalizePath(path)
			}
			normalized = append(normalized, path)
		}
		if reflect.DeepEqual(normalized, expected.Files) {
			return nil
		}
		return fmt.Errorf("file_change.files = %v, want %v", collector.filePaths, expected.Files)
	}
	return nil
}

func assertReplayCommandExpectation(expected CommandExpectation, collector *Collector) error {
	if collector.commandCount != expected.NumMessages {
		return fmt.Errorf("command.num_messages = %d, want %d", collector.commandCount, expected.NumMessages)
	}
	if !reflect.DeepEqual(collector.commands, expected.Executed) {
		return fmt.Errorf("command.executed = %v, want %v", collector.commands, expected.Executed)
	}
	return nil
}

func assertReplayCommandStreams(expected CommandExpectation, streams map[agent.StreamID]commandStreamState) error {
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

func assertReplayFileChangeStreams(expected FileChangeExpectation, streams map[agent.StreamID]toolStreamState) error {
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

// NewCollector creates a scenario collector with caller-supplied backend rules.
func NewCollector(options CollectorOptions) *Collector { return &Collector{options: options} }

// Expectations returns the observed recording summary.
func (c *Collector) Expectations(actions []Action, approvals []ApprovalExpectation) Expect {
	numStreams, allFlushed := replayReasoningSummary(c.reasoningStreams)
	paths := slices.Clone(c.filePaths)
	if c.options.NormalizePath != nil {
		paths = nil
		for _, path := range c.filePaths {
			replayAppendUnique(&paths, c.options.NormalizePath(path))
		}
	}
	var notice *NoticeExpectation
	for _, action := range actions {
		if action.Kind == "notice" {
			notice = &NoticeExpectation{NumTurnFlushes: c.noticeTurnFlushes}
			break
		}
	}
	return Expect{
		Output:     TextExpectation{NumMessages: c.outputCount, Content: c.outputText.String()},
		Log:        TextExpectation{NumMessages: c.logCount, Content: c.logText.String()},
		Reasoning:  ReasoningExpectation{NumMessages: c.reasoningCount, Content: c.reasoningText.String(), NumStreams: numStreams, AllFlushed: allFlushed},
		FileChange: FileChangeExpectation{NumMessages: c.fileChangeCount, Files: paths},
		Command:    CommandExpectation{NumMessages: c.commandCount, Executed: slices.Clone(c.commands)},
		Notice:     notice, Approvals: approvals,
	}
}
