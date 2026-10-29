package transcript

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
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
	fields := []struct{ key, value string }{
		{"name", output.Name}, {"backend", output.Backend}, {"backend_version", output.BackendVersion},
		{"model", output.Model}, {"effort", output.Effort}, {"system_prompt", output.SystemPrompt},
	}
	for _, field := range fields {
		if field.value == "" && field.key != "name" {
			continue
		}
		if err := writeHeaderField(w, field.key, strconv.Quote(field.value)); err != nil {
			return err
		}
	}
	if len(output.Options) == 0 {
		return nil
	}
	encoder := yaml.NewEncoder(w)
	encoder.SetIndent(2)
	if err := encoder.Encode(struct {
		Options map[string]string `yaml:"options"`
	}{output.Options}); err != nil {
		return fmt.Errorf("encode options: %w", err)
	}
	if err := encoder.Close(); err != nil {
		return fmt.Errorf("close options encoder: %w", err)
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
	output := Output{}
	output.Expect.Reasoning.AllFlushed = true
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	decoder.KnownFields(true)
	if err := decoder.Decode(&output); err != nil {
		return Output{}, fmt.Errorf("parse transcript front matter: %w", err)
	}
	// Preserve the canonical nil representation used by collectors for empty lists.
	if len(output.Expect.Approvals) == 0 {
		output.Expect.Approvals = nil
	}
	if len(output.Expect.Command.Executed) == 0 {
		output.Expect.Command.Executed = nil
	}
	if len(output.Expect.FileChange.Files) == 0 {
		output.Expect.FileChange.Files = nil
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
