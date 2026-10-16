package claude

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/roomscript/coderoom/internal/agent"
)

// turnSignal tells the client how a line affects the turn lifecycle.
type turnSignal int

const (
	signalNone   turnSignal = iota
	signalInit              // system/init: a turn is starting
	signalResult            // result: the turn is over
)

// decoded is everything the client needs from one stdout line.
type decoded struct {
	msgs            []agent.Message
	signal          turnSignal
	result          resultLine
	controlRequest  *controlRequestLine
	controlResponse *controlResponseLine
}

type blockKind int

const (
	blockKindOutput blockKind = iota
	blockKindReasoning
)

// openBlock is a text or thinking block of the current assistant message.
type openBlock struct {
	id      agent.StreamID
	kind    blockKind
	emitted bool // at least one ModeStream fragment was emitted
}

type toolKind int

const (
	toolKindCommand toolKind = iota
	toolKindFileChange
)

// toolCall is a mapped tool_use waiting for its tool_result.
type toolCall struct {
	id   agent.StreamID
	kind toolKind
	path string
}

// decoder translates stdout lines into agent messages. It is owned by the
// stdout reader goroutine and is not safe for concurrent use.
type decoder struct {
	messageID string
	blocks    map[int]*openBlock
	tools     map[string]toolCall
	declined  map[string]bool // tool_use_ids whose permission was denied
}

func newDecoder() *decoder {
	return &decoder{
		blocks:   map[int]*openBlock{},
		tools:    map[string]toolCall{},
		declined: map[string]bool{},
	}
}

// markDeclined records that the host denied the tool call, so its result is
// reported as declined rather than failed.
func (d *decoder) markDeclined(toolUseID string) {
	if toolUseID != "" {
		d.declined[toolUseID] = true
	}
}

func (d *decoder) decode(raw []byte) (decoded, error) {
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return decoded{}, fmt.Errorf("decode envelope: %w", err)
	}
	switch env.Type {
	case typeStreamEvent:
		return d.decodeStreamEvent(raw)
	case typeAssistant:
		return d.decodeAssistant(raw)
	case typeUser:
		return d.decodeUser(raw)
	case typeSystem:
		if env.Subtype == subtypeInit {
			return decoded{signal: signalInit}, nil
		}
		return decoded{}, nil
	case typeResult:
		return d.decodeResult(raw)
	case typeControlRequest:
		return decodeControlRequest(raw)
	case typeControlResponse:
		return decodeControlResponse(raw)
	}
	return decoded{}, nil
}

func decodeControlRequest(raw []byte) (decoded, error) {
	var line controlRequestLine
	if err := json.Unmarshal(raw, &line); err != nil {
		return decoded{}, fmt.Errorf("decode control request: %w", err)
	}
	return decoded{controlRequest: &line}, nil
}

func decodeControlResponse(raw []byte) (decoded, error) {
	var line controlResponseLine
	if err := json.Unmarshal(raw, &line); err != nil {
		return decoded{}, fmt.Errorf("decode control response: %w", err)
	}
	return decoded{controlResponse: &line}, nil
}

func (d *decoder) decodeStreamEvent(raw []byte) (decoded, error) {
	var line streamEventLine
	if err := json.Unmarshal(raw, &line); err != nil {
		return decoded{}, fmt.Errorf("decode stream event: %w", err)
	}
	ev := line.Event
	switch ev.Type {
	case eventMessageStart:
		d.messageID = ev.Message.ID
	case eventContentBlockStart:
		d.startBlock(ev)
	case eventContentBlockDelta:
		return decoded{msgs: d.blockDelta(ev)}, nil
	case eventContentBlockStop:
		return decoded{msgs: d.stopBlock(ev.Index)}, nil
	}
	return decoded{}, nil
}

func (d *decoder) startBlock(ev streamEvent) {
	switch ev.ContentBlock.Type {
	case blockText:
		d.blocks[ev.Index] = &openBlock{id: d.blockStreamID("output", ev.Index), kind: blockKindOutput}
	case blockThinking:
		d.blocks[ev.Index] = &openBlock{id: d.blockStreamID("reasoning", ev.Index), kind: blockKindReasoning}
	}
}

func (d *decoder) blockStreamID(kind string, index int) agent.StreamID {
	return agent.StreamID(fmt.Sprintf("claude:%s:%s:%d", kind, d.messageID, index))
}

func (d *decoder) blockDelta(ev streamEvent) []agent.Message {
	b, ok := d.blocks[ev.Index]
	if !ok {
		return nil
	}
	var content agent.MessageContent
	switch {
	case b.kind == blockKindOutput && ev.Delta.Type == deltaText && ev.Delta.Text != "":
		content = agent.Output{Text: ev.Delta.Text}
	case b.kind == blockKindReasoning && ev.Delta.Type == deltaThinking && ev.Delta.Thinking != "":
		content = agent.Reasoning{Text: ev.Delta.Thinking}
	default:
		// Signature deltas and empty thinking deltas (no summary) carry no text.
		return nil
	}
	b.emitted = true
	return []agent.Message{{StreamID: b.id, Mode: agent.ModeStream, Content: content}}
}

func (d *decoder) stopBlock(index int) []agent.Message {
	b, ok := d.blocks[index]
	if !ok {
		return nil
	}
	delete(d.blocks, index)
	if !b.emitted {
		return nil
	}
	return []agent.Message{blockFlush(b)}
}

func blockFlush(b *openBlock) agent.Message {
	if b.kind == blockKindReasoning {
		return agent.Message{StreamID: b.id, Mode: agent.ModeFlush, Content: agent.Reasoning{}}
	}
	return agent.Message{StreamID: b.id, Mode: agent.ModeFlush, Content: agent.Output{}}
}

func (d *decoder) decodeAssistant(raw []byte) (decoded, error) {
	var line assistantLine
	if err := json.Unmarshal(raw, &line); err != nil {
		return decoded{}, fmt.Errorf("decode assistant message: %w", err)
	}
	var msgs []agent.Message
	for _, block := range line.Message.Content {
		if block.Type != blockToolUse {
			continue
		}
		msgs = append(msgs, d.startTool(block)...)
	}
	return decoded{msgs: msgs}, nil
}

func (d *decoder) startTool(block contentBlock) []agent.Message {
	switch block.Name {
	case "Bash":
		var in bashInput
		_ = json.Unmarshal(block.Input, &in)
		tc := toolCall{id: agent.StreamID("claude:command:" + block.ID), kind: toolKindCommand}
		d.tools[block.ID] = tc
		return []agent.Message{{StreamID: tc.id, Mode: agent.ModeStream, Content: agent.Command{Command: in.Command}}}
	case "Write", "Edit", "NotebookEdit":
		var in fileInput
		_ = json.Unmarshal(block.Input, &in)
		path := in.FilePath
		if path == "" {
			path = in.NotebookPath
		}
		tc := toolCall{id: agent.StreamID("claude:filechange:" + block.ID), kind: toolKindFileChange, path: path}
		d.tools[block.ID] = tc
		return []agent.Message{{StreamID: tc.id, Mode: agent.ModeStream, Content: agent.FileChangeSet{Status: agent.ToolStatusInProgress}}}
	}
	return nil
}

func (d *decoder) decodeUser(raw []byte) (decoded, error) {
	var line userLine
	if err := json.Unmarshal(raw, &line); err != nil {
		return decoded{}, fmt.Errorf("decode user message: %w", err)
	}
	if isJSONString(line.Message.Content) {
		// Our own prompt replayed back. Nothing to emit.
		return decoded{}, nil
	}
	var blocks []toolResultBlock
	if err := json.Unmarshal(line.Message.Content, &blocks); err != nil {
		return decoded{}, fmt.Errorf("decode user content: %w", err)
	}
	var msgs []agent.Message
	for _, block := range blocks {
		if block.Type != "tool_result" {
			continue
		}
		msgs = append(msgs, d.finishTool(block, line.ToolUseResult)...)
	}
	return decoded{msgs: msgs}, nil
}

func (d *decoder) finishTool(block toolResultBlock, toolUseResult json.RawMessage) []agent.Message {
	tc, ok := d.tools[block.ToolUseID]
	if !ok {
		return nil
	}
	delete(d.tools, block.ToolUseID)
	declined := d.declined[block.ToolUseID]
	delete(d.declined, block.ToolUseID)

	if tc.kind == toolKindCommand {
		return commandResultMessages(tc, block, toolUseResult)
	}
	return fileChangeResultMessages(tc, block, toolUseResult, declined)
}

// unknownExitCode marks a failed command whose exit code Claude did not
// report. It follows os.ProcessState.ExitCode, which returns -1 when there is
// no normal exit status.
const unknownExitCode = -1

// exitCodeLine matches the first line of a failed Bash result.
var exitCodeLine = regexp.MustCompile(`^Exit code (-?\d+)(?:\n|$)`)

func commandResultMessages(tc toolCall, block toolResultBlock, toolUseResult json.RawMessage) []agent.Message {
	out, exitCode := parseCommandResult(block, toolUseResult)
	completed := agent.CommandWithOverrideOutput(out)
	completed.ExitCode = exitCode
	return []agent.Message{
		{StreamID: tc.id, Mode: agent.ModeStream, Content: completed},
		toolFlush(tc),
	}
}

// parseCommandResult returns the Bash output and exit code. Claude reports
// the two outcomes in different shapes:
//   - success: a structured result with separate stdout and stderr, exit 0.
//     Some non-zero exits also count as success (grep with no match) and
//     carry returnCodeInterpretation instead of the code, which stays nil.
//   - failure: only text, "Exit code N" followed by stdout and stderr merged.
//     Failures without that line (denied, blocked by settings, interrupted)
//     get unknownExitCode.
func parseCommandResult(block toolResultBlock, toolUseResult json.RawMessage) (string, *int) {
	text := parseResultText(block.Content)
	if block.IsError {
		return parseCommandFailure(text)
	}
	var r bashResult
	if isJSONNull(toolUseResult) || json.Unmarshal(toolUseResult, &r) != nil {
		return text, nil
	}
	out := joinNonEmpty(r.Stdout, r.Stderr)
	if r.ReturnCodeInterpretation != "" {
		return joinNonEmpty(out, "["+r.ReturnCodeInterpretation+"]"), nil
	}
	return out, intPtr(0)
}

// parseCommandFailure splits a failed Bash result into its output and the
// exit code from the leading "Exit code N" line.
func parseCommandFailure(text string) (string, *int) {
	m := exitCodeLine.FindStringSubmatch(text)
	if m == nil {
		return text, intPtr(unknownExitCode)
	}
	code, err := strconv.Atoi(m[1])
	if err != nil {
		return text, intPtr(unknownExitCode)
	}
	return text[len(m[0]):], &code
}

func fileChangeResultMessages(tc toolCall, block toolResultBlock, toolUseResult json.RawMessage, declined bool) []agent.Message {
	status := agent.ToolStatusCompleted
	switch {
	case declined:
		status = agent.ToolStatusDeclined
	case block.IsError:
		status = agent.ToolStatusFailed
	}
	set := agent.FileChangeSet{Status: status, Changes: parseFileChanges(tc, toolUseResult, status)}
	return []agent.Message{
		{StreamID: tc.id, Mode: agent.ModeStream, Content: set},
		toolFlush(tc),
	}
}

// parseFileChanges maps a Write/Edit structured result to file changes. A
// created file carries its content as the diff, as the Codex adapter does; an
// update carries unified diff hunks.
func parseFileChanges(tc toolCall, toolUseResult json.RawMessage, status agent.ToolStatus) []agent.FileChange {
	var r fileResult
	if status != agent.ToolStatusCompleted || json.Unmarshal(toolUseResult, &r) != nil {
		return []agent.FileChange{{Path: tc.path}}
	}
	path := r.FilePath
	if path == "" {
		path = tc.path
	}
	if r.Type == "create" || (r.OriginalFile == nil && len(r.StructuredPatch) == 0) {
		return []agent.FileChange{{Path: path, Diff: r.Content, ChangeKind: "add"}}
	}
	return []agent.FileChange{{Path: path, Diff: renderHunks(r.StructuredPatch), ChangeKind: "update"}}
}

func renderHunks(hunks []hunk) string {
	var sb strings.Builder
	for _, h := range hunks {
		fmt.Fprintf(&sb, "@@ -%d,%d +%d,%d @@\n", h.OldStart, h.OldLines, h.NewStart, h.NewLines)
		for _, l := range h.Lines {
			sb.WriteString(l)
			sb.WriteByte('\n')
		}
	}
	return sb.String()
}

func (d *decoder) decodeResult(raw []byte) (decoded, error) {
	var line resultLine
	if err := json.Unmarshal(raw, &line); err != nil {
		return decoded{}, fmt.Errorf("decode result: %w", err)
	}
	return decoded{msgs: d.flushOpen(), signal: signalResult, result: line}, nil
}

// flushOpen closes every stream still open at the end of a turn, for example
// after an interrupt.
func (d *decoder) flushOpen() []agent.Message {
	var msgs []agent.Message
	for _, index := range slices.Sorted(maps.Keys(d.blocks)) {
		if b := d.blocks[index]; b.emitted {
			msgs = append(msgs, blockFlush(b))
		}
	}
	for _, id := range slices.Sorted(maps.Keys(d.tools)) {
		msgs = append(msgs, toolFlush(d.tools[id]))
	}
	clear(d.blocks)
	clear(d.tools)
	clear(d.declined)
	return msgs
}

func toolFlush(tc toolCall) agent.Message {
	if tc.kind == toolKindCommand {
		return agent.Message{StreamID: tc.id, Mode: agent.ModeFlush, Content: agent.Command{}}
	}
	return agent.Message{StreamID: tc.id, Mode: agent.ModeFlush, Content: agent.FileChangeSet{}}
}

// parseResultText returns tool_result content, which is either a string or a
// list of text blocks.
func parseResultText(content json.RawMessage) string {
	var s string
	if json.Unmarshal(content, &s) == nil {
		return s
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(content, &parts) != nil {
		return ""
	}
	var texts []string
	for _, p := range parts {
		if p.Type == "text" {
			texts = append(texts, p.Text)
		}
	}
	return strings.Join(texts, "\n")
}

func isJSONNull(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null"))
}

func intPtr(v int) *int { return &v }

func isJSONString(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 0 && trimmed[0] == '"'
}

func joinNonEmpty(parts ...string) string {
	var out []string
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, "\n")
}
