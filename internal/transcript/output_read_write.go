package transcript

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/roomscript/coderoom/internal/agent"
)

// WriteOutput serializes a recorded transcript fixture to front matter plus JSONL steps.
func WriteOutput(w io.Writer, output Output, steps []Step) error {
	if err := writeFrontMatter(w, output); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "---"); err != nil {
		return fmt.Errorf("write transcript delimiter: %w", err)
	}
	enc := json.NewEncoder(w)
	for _, step := range steps {
		if err := enc.Encode(step); err != nil {
			return fmt.Errorf("encode transcript step: %w", err)
		}
	}
	return nil
}

// Write serializes a transcript fixture to front matter plus JSONL steps.
func Write(w io.Writer, output Output, steps []Step) error {
	return WriteOutput(w, output, steps)
}

// ReadOutput parses one transcript fixture from front matter plus JSONL steps.
func ReadOutput(r io.Reader) (Output, []Step, error) {
	body, err := io.ReadAll(r)
	if err != nil {
		return Output{}, nil, fmt.Errorf("read transcript: %w", err)
	}
	parts := bytes.SplitN(body, []byte("\n---\n"), 2)
	if len(parts) != 2 {
		return Output{}, nil, fmt.Errorf("transcript: missing front matter delimiter")
	}
	output, err := parseFrontMatter(parts[0])
	if err != nil {
		return Output{}, nil, err
	}
	steps, err := parseSteps(parts[1])
	if err != nil {
		return Output{}, nil, err
	}
	return output, steps, nil
}

// Read parses one transcript fixture from front matter plus JSONL steps.
func Read(r io.Reader) (Output, []Step, error) {
	return ReadOutput(r)
}

func parseSteps(body []byte) ([]Step, error) {
	scanner := bufio.NewScanner(bytes.NewReader(body))
	var steps []Step
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var step Step
		if err := json.Unmarshal([]byte(line), &step); err != nil {
			return nil, fmt.Errorf("parse transcript step: %w", err)
		}
		steps = append(steps, step)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan transcript steps: %w", err)
	}
	return steps, nil
}

func writeFrontMatter(w io.Writer, output Output) error {
	if err := writeHeader(w, output); err != nil {
		return err
	}
	if err := writeExpectations(w, output.Expect); err != nil {
		return err
	}
	return nil
}

func writeHeader(w io.Writer, output Output) error {
	if _, err := fmt.Fprintln(w, "---"); err != nil {
		return fmt.Errorf("write front matter start: %w", err)
	}
	if err := writeHeaderFields(w, output); err != nil {
		return err
	}
	if err := writeActions(w, NormalizedActions(output)); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "expect:"); err != nil {
		return fmt.Errorf("write expect header: %w", err)
	}
	return nil
}

func writeHeaderFields(w io.Writer, output Output) error {
	if err := writeHeaderField(w, "name", output.Name); err != nil {
		return fmt.Errorf("write transcript name: %w", err)
	}
	if output.CodexVersion != "" {
		if err := writeHeaderField(w, "codex_version", output.CodexVersion); err != nil {
			return fmt.Errorf("write codex version: %w", err)
		}
	}
	if output.Model != "" {
		if err := writeHeaderField(w, "model", output.Model); err != nil {
			return fmt.Errorf("write model: %w", err)
		}
	}
	if output.DeveloperInstructions != "" {
		if err := writeHeaderField(w, "developer_instructions", strconv.Quote(output.DeveloperInstructions)); err != nil {
			return fmt.Errorf("write developer instructions: %w", err)
		}
	}
	return nil
}

func writeHeaderField(w io.Writer, key, value string) error {
	_, err := fmt.Fprintf(w, "%s: %s\n", key, value)
	if err != nil {
		return fmt.Errorf("write header field %q: %w", key, err)
	}
	return nil
}

func writeActions(w io.Writer, actions []Action) error {
	if len(actions) == 0 {
		return nil
	}
	if _, err := fmt.Fprintln(w, "actions:"); err != nil {
		return fmt.Errorf("write actions header: %w", err)
	}
	for _, action := range actions {
		if _, err := fmt.Fprintf(w, "  - kind: %s\n    text: %s\n", action.Kind, strconv.Quote(action.Text)); err != nil {
			return fmt.Errorf("write action: %w", err)
		}
	}
	return nil
}

func writeExpectations(w io.Writer, expect Expect) error {
	if err := writeTextExpectation(w, "  output", expect.Output); err != nil {
		return err
	}
	if err := writeTextExpectation(w, "  log", expect.Log); err != nil {
		return err
	}
	if err := writeReasoningExpectation(w, expect.Reasoning); err != nil {
		return err
	}
	if err := writeFileChangeExpectation(w, expect.FileChange); err != nil {
		return err
	}
	if err := writeCommandExpectation(w, expect.Command); err != nil {
		return err
	}
	if err := writeNoticeExpectation(w, expect.Notice); err != nil {
		return err
	}
	if err := writeApprovalExpectations(w, expect.Approvals); err != nil {
		return err
	}
	return nil
}

func writeFileChangeExpectation(w io.Writer, expect FileChangeExpectation) error {
	if _, err := fmt.Fprintf(w, "  file_change:\n    num_messages: %d\n", expect.NumMessages); err != nil {
		return fmt.Errorf("write file change count: %w", err)
	}
	return writeStringList(w, "    files", expect.Files, false)
}

func writeCommandExpectation(w io.Writer, expect CommandExpectation) error {
	if _, err := fmt.Fprintf(w, "  command:\n    num_messages: %d\n", expect.NumMessages); err != nil {
		return fmt.Errorf("write command count: %w", err)
	}
	return writeStringList(w, "    executed", expect.Executed, true)
}

func writeNoticeExpectation(w io.Writer, expect *NoticeExpectation) error {
	if expect == nil {
		return nil
	}
	if _, err := fmt.Fprintf(w, "  notice:\n    num_turn_flushes: %d\n", expect.NumTurnFlushes); err != nil {
		return fmt.Errorf("write notice expectation: %w", err)
	}
	return nil
}

func writeApprovalExpectations(w io.Writer, approvals []ApprovalExpectation) error {
	if len(approvals) == 0 {
		if _, err := fmt.Fprintln(w, "  approvals: []"); err != nil {
			return fmt.Errorf("write empty approvals: %w", err)
		}
		return nil
	}
	if _, err := fmt.Fprintln(w, "  approvals:"); err != nil {
		return fmt.Errorf("write approvals header: %w", err)
	}
	for _, approval := range approvals {
		if _, err := fmt.Fprintf(w, "    - kind: %s\n      decision: %s\n", approval.Kind, approval.Decision); err != nil {
			return fmt.Errorf("write approval expectation: %w", err)
		}
	}
	return nil
}

func writeStringList(w io.Writer, key string, values []string, quote bool) error {
	if len(values) == 0 {
		if _, err := fmt.Fprintf(w, "%s: []\n", key); err != nil {
			return fmt.Errorf("write empty list %s: %w", strings.TrimSpace(key), err)
		}
		return nil
	}
	if _, err := fmt.Fprintf(w, "%s:\n", key); err != nil {
		return fmt.Errorf("write list header %s: %w", strings.TrimSpace(key), err)
	}
	for _, value := range values {
		rendered := value
		if quote {
			rendered = strconv.Quote(value)
		}
		if _, err := fmt.Fprintf(w, "      - %s\n", rendered); err != nil {
			return fmt.Errorf("write list item %s: %w", strings.TrimSpace(key), err)
		}
	}
	return nil
}

func writeTextExpectation(w io.Writer, key string, v TextExpectation) error {
	_, err := fmt.Fprintf(w, "%s:\n    num_messages: %d\n    content: %s\n", key, v.NumMessages, strconv.Quote(v.Content))
	if err != nil {
		return fmt.Errorf("write text expectation %s: %w", strings.TrimSpace(key), err)
	}
	return nil
}

func writeReasoningExpectation(w io.Writer, v ReasoningExpectation) error {
	_, err := fmt.Fprintf(
		w,
		"  reasoning:\n    num_messages: %d\n    content: %s\n    num_streams: %d\n    all_flushed: %t\n",
		v.NumMessages,
		strconv.Quote(v.Content),
		v.NumStreams,
		v.AllFlushed,
	)
	if err != nil {
		return fmt.Errorf("write reasoning expectation: %w", err)
	}
	return nil
}

func parseFrontMatter(raw []byte) (Output, error) {
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	output := Output{}
	output.Expect.Reasoning.AllFlushed = true
	output.Actions = nil
	state := frontMatterStateRoot
	for scanner.Scan() {
		nextState, err := applyFrontMatterLine(&output, state, scanner.Text())
		if err != nil {
			return Output{}, err
		}
		state = nextState
	}
	if err := scanner.Err(); err != nil {
		return Output{}, fmt.Errorf("scan front matter: %w", err)
	}
	if len(output.Actions) == 0 && output.Input != "" {
		output.Actions = NormalizedActions(output)
	}
	if output.Expect.Notice == nil {
		if n := DefaultNoticeTurnFlushes(output); n > 0 {
			output.Expect.Notice = &NoticeExpectation{NumTurnFlushes: n}
		}
	}
	return output, nil
}

type frontMatterState string

const (
	frontMatterStateRoot       frontMatterState = "root"
	frontMatterStateActions    frontMatterState = "actions"
	frontMatterStateOutput     frontMatterState = "output"
	frontMatterStateLog        frontMatterState = "log"
	frontMatterStateReasoning  frontMatterState = "reasoning"
	frontMatterStateFileChange frontMatterState = "file_change"
	frontMatterStateCommand    frontMatterState = "command"
	frontMatterStateNotice     frontMatterState = "notice"
	frontMatterStateApprovals  frontMatterState = "approvals"
)

func applyFrontMatterLine(output *Output, state frontMatterState, line string) (frontMatterState, error) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || trimmed == "---" || trimmed == "expect:" {
		return state, nil
	}
	if nextState, matched := parseFrontMatterSection(trimmed); matched {
		return applyFrontMatterSection(output, trimmed, nextState), nil
	}
	if matched, err := applyFrontMatterHeader(output, trimmed); matched {
		return state, err
	}
	return applyFrontMatterStatefulLine(output, state, trimmed)
}

func applyFrontMatterSection(output *Output, trimmed string, nextState frontMatterState) frontMatterState {
	if nextState == frontMatterStateNotice && output.Expect.Notice == nil {
		output.Expect.Notice = &NoticeExpectation{}
	}
	applyFrontMatterEmptySection(output, trimmed)
	return nextState
}

func applyFrontMatterHeader(output *Output, trimmed string) (bool, error) {
	if strings.HasPrefix(trimmed, "name: ") {
		output.Name = strings.TrimSpace(strings.TrimPrefix(trimmed, "name: "))
		return true, nil
	}
	if strings.HasPrefix(trimmed, "codex_version: ") {
		output.CodexVersion = strings.TrimSpace(strings.TrimPrefix(trimmed, "codex_version: "))
		return true, nil
	}
	if strings.HasPrefix(trimmed, "model: ") {
		output.Model = strings.TrimSpace(strings.TrimPrefix(trimmed, "model: "))
		return true, nil
	}
	if strings.HasPrefix(trimmed, "developer_instructions: ") {
		value, err := parseQuotedValue(trimmed, "developer_instructions: ")
		if err != nil {
			return true, err
		}
		output.DeveloperInstructions = value
		return true, nil
	}
	if strings.HasPrefix(trimmed, "input: ") {
		input, err := parseQuotedValue(trimmed, "input: ")
		if err != nil {
			return true, err
		}
		output.Input = input
		return true, nil
	}
	return false, nil
}

func parseFrontMatterSection(trimmed string) (frontMatterState, bool) {
	switch {
	case strings.HasPrefix(trimmed, "actions:"):
		return frontMatterStateActions, true
	case strings.HasPrefix(trimmed, "output:"):
		return frontMatterStateOutput, true
	case strings.HasPrefix(trimmed, "log:"):
		return frontMatterStateLog, true
	case strings.HasPrefix(trimmed, "reasoning:"):
		return frontMatterStateReasoning, true
	case strings.HasPrefix(trimmed, "file_change:"):
		return frontMatterStateFileChange, true
	case strings.HasPrefix(trimmed, "command:"):
		return frontMatterStateCommand, true
	case strings.HasPrefix(trimmed, "notice:"):
		return frontMatterStateNotice, true
	case strings.HasPrefix(trimmed, "approvals:"):
		return frontMatterStateApprovals, true
	default:
		return "", false
	}
}

func applyFrontMatterEmptySection(output *Output, trimmed string) {
	switch trimmed {
	case "approvals: []":
		output.Expect.Approvals = nil
	case "files: []":
		output.Expect.FileChange.Files = nil
	case "executed: []":
		output.Expect.Command.Executed = nil
	}
}

func applyFrontMatterStatefulLine(output *Output, state frontMatterState, trimmed string) (frontMatterState, error) {
	if state == frontMatterStateActions {
		return state, parseActionLine(output, trimmed)
	}
	if matched, err := parseFrontMatterScalarLine(output, state, trimmed); matched {
		return state, err
	}
	return state, nil
}

func parseFrontMatterScalarLine(output *Output, state frontMatterState, trimmed string) (bool, error) {
	if strings.HasPrefix(trimmed, "num_messages: ") {
		return true, parseNumMessages(output, state, trimmed)
	}
	if strings.HasPrefix(trimmed, "num_turn_flushes: ") {
		return true, parseNoticeNumTurnFlushes(output, state, trimmed)
	}
	if strings.HasPrefix(trimmed, "content: ") {
		return true, parseContent(output, state, trimmed)
	}
	if strings.HasPrefix(trimmed, "num_streams: ") {
		return true, parseReasoningNumStreams(output, state, trimmed)
	}
	if strings.HasPrefix(trimmed, "all_flushed: ") {
		return true, parseReasoningAllFlushed(output, state, trimmed)
	}
	if trimmed == "files: []" {
		output.Expect.FileChange.Files = nil
		return true, nil
	}
	if trimmed == "executed: []" {
		output.Expect.Command.Executed = nil
		return true, nil
	}
	if strings.HasPrefix(trimmed, "- ") {
		return true, parseListItem(output, state, trimmed)
	}
	if strings.HasPrefix(trimmed, "decision: ") {
		return true, parseApprovalDecision(output, trimmed)
	}
	return false, nil
}

func parseActionLine(output *Output, trimmed string) error {
	switch {
	case strings.HasPrefix(trimmed, "- kind: "):
		parseActionKind(output, trimmed)
		return nil
	case strings.HasPrefix(trimmed, "text: "):
		return parseActionText(output, trimmed)
	default:
		return nil
	}
}

func parseNumMessages(output *Output, state frontMatterState, trimmed string) error {
	n, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(trimmed, "num_messages: ")))
	if err != nil {
		return fmt.Errorf("parse num_messages: %w", err)
	}
	switch state {
	case frontMatterStateOutput:
		output.Expect.Output.NumMessages = n
	case frontMatterStateLog:
		output.Expect.Log.NumMessages = n
	case frontMatterStateReasoning:
		output.Expect.Reasoning.NumMessages = n
	case frontMatterStateFileChange:
		output.Expect.FileChange.NumMessages = n
	case frontMatterStateCommand:
		output.Expect.Command.NumMessages = n
	case frontMatterStateRoot, frontMatterStateActions, frontMatterStateNotice, frontMatterStateApprovals:
	}
	return nil
}

func parseContent(output *Output, state frontMatterState, trimmed string) error {
	content, err := parseQuotedValue(trimmed, "content: ")
	if err != nil {
		return err
	}
	switch state {
	case frontMatterStateOutput:
		output.Expect.Output.Content = content
	case frontMatterStateLog:
		output.Expect.Log.Content = content
	case frontMatterStateReasoning:
		output.Expect.Reasoning.Content = content
	case frontMatterStateRoot, frontMatterStateActions, frontMatterStateFileChange, frontMatterStateCommand, frontMatterStateNotice, frontMatterStateApprovals:
	}
	return nil
}

func parseNoticeNumTurnFlushes(output *Output, state frontMatterState, trimmed string) error {
	if state != frontMatterStateNotice {
		return nil
	}
	if output.Expect.Notice == nil {
		output.Expect.Notice = &NoticeExpectation{}
	}
	n, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(trimmed, "num_turn_flushes: ")))
	if err != nil {
		return fmt.Errorf("parse num_turn_flushes: %w", err)
	}
	output.Expect.Notice.NumTurnFlushes = n
	return nil
}

func parseReasoningNumStreams(output *Output, state frontMatterState, trimmed string) error {
	if state != frontMatterStateReasoning {
		return nil
	}
	n, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(trimmed, "num_streams: ")))
	if err != nil {
		return fmt.Errorf("parse num_streams: %w", err)
	}
	output.Expect.Reasoning.NumStreams = n
	return nil
}

func parseReasoningAllFlushed(output *Output, state frontMatterState, trimmed string) error {
	if state != frontMatterStateReasoning {
		return nil
	}
	value, err := strconv.ParseBool(strings.TrimSpace(strings.TrimPrefix(trimmed, "all_flushed: ")))
	if err != nil {
		return fmt.Errorf("parse all_flushed: %w", err)
	}
	output.Expect.Reasoning.AllFlushed = value
	return nil
}

func parseListItem(output *Output, state frontMatterState, trimmed string) error {
	switch state {
	case frontMatterStateActions:
		return nil
	case frontMatterStateFileChange:
		output.Expect.FileChange.Files = append(output.Expect.FileChange.Files, strings.TrimSpace(strings.TrimPrefix(trimmed, "- ")))
		return nil
	case frontMatterStateCommand:
		cmd, err := parseQuotedValue(trimmed, "- ")
		if err != nil {
			return fmt.Errorf("parse executed command: %w", err)
		}
		output.Expect.Command.Executed = append(output.Expect.Command.Executed, cmd)
		return nil
	case frontMatterStateApprovals:
		output.Expect.Approvals = append(output.Expect.Approvals, ApprovalExpectation{
			Kind: agent.ApprovalKind(strings.TrimSpace(strings.TrimPrefix(trimmed, "- kind: "))),
		})
		return nil
	default:
		return nil
	}
}

func parseActionKind(output *Output, trimmed string) {
	output.Actions = append(output.Actions, Action{
		Kind: strings.TrimSpace(strings.TrimPrefix(trimmed, "- kind: ")),
	})
}

func parseActionText(output *Output, trimmed string) error {
	if len(output.Actions) == 0 {
		return fmt.Errorf("parse action text: missing action kind")
	}
	text, err := parseQuotedValue(trimmed, "text: ")
	if err != nil {
		return err
	}
	output.Actions[len(output.Actions)-1].Text = text
	return nil
}

func parseApprovalDecision(output *Output, trimmed string) error {
	if len(output.Expect.Approvals) == 0 {
		return fmt.Errorf("parse decision: missing approval kind")
	}
	output.Expect.Approvals[len(output.Expect.Approvals)-1].Decision = agent.ApprovalOption(strings.TrimSpace(strings.TrimPrefix(trimmed, "decision: ")))
	return nil
}

func parseQuotedValue(trimmed, prefix string) (string, error) {
	value := strings.TrimSpace(strings.TrimPrefix(trimmed, prefix))
	decoded, err := strconv.Unquote(value)
	if err != nil {
		return "", fmt.Errorf("parse quoted value %q: %w", prefix, err)
	}
	return decoded, nil
}
