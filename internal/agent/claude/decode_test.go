package claude

import (
	"strconv"
	"testing"

	"github.com/roomscript/coderoom/internal/agent"
)

// Protocol lines below follow the shapes recorded in
// docs/playground/poc-claude-stream-json.md, trimmed to the fields the
// decoder reads.

const (
	lineMessageStart = `{"type":"stream_event","event":{"type":"message_start","message":{"id":"msg_1"}}}`
	lineTextStart    = `{"type":"stream_event","event":{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}}`
	lineThinkStart   = `{"type":"stream_event","event":{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}}`
	lineBlockStop    = `{"type":"stream_event","event":{"type":"content_block_stop","index":0}}`
	lineSystemInit   = `{"type":"system","subtype":"init","session_id":"s1","model":"claude-haiku-5-5"}`
	lineResultOK     = `{"type":"result","subtype":"success","is_error":false,"result":"ok","terminal_reason":"completed"}`
)

func textDeltaLine(text string) string {
	return `{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"` + text + `"}}}`
}

func thinkingDeltaLine(text string) string {
	return `{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"` + text + `"}}}`
}

func toolUseLine(id, name, input string) string {
	return `{"type":"assistant","message":{"id":"msg_1","content":[{"type":"tool_use","id":"` + id + `","name":"` + name + `","input":` + input + `}]}}`
}

func toolResultLine(id, content string, isError bool, toolUseResult string) string {
	errField := "false"
	if isError {
		errField = "true"
	}
	return `{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"` + id +
		`","content":` + content + `,"is_error":` + errField + `}]},"tool_use_result":` + toolUseResult + `}`
}

// decodeAll feeds lines through one decoder and returns all messages.
func decodeAll(t *testing.T, d *decoder, lines ...string) []agent.Message {
	t.Helper()
	var msgs []agent.Message
	for _, l := range lines {
		out, err := d.decode([]byte(l))
		if err != nil {
			t.Fatalf("decode %s: %v", l, err)
		}
		msgs = append(msgs, out.msgs...)
	}
	return msgs
}

// accumulate folds a stream into its final message and checks it is flushed.
func accumulate(t *testing.T, msgs []agent.Message) agent.Message {
	t.Helper()
	if len(msgs) == 0 {
		t.Fatal("no messages")
	}
	acc := msgs[0]
	for _, m := range msgs[1:] {
		var err error
		if acc, err = acc.Accumulate(m); err != nil {
			t.Fatalf("accumulate: %v", err)
		}
	}
	if acc.Mode != agent.ModeFlush {
		t.Fatalf("stream not flushed: last mode %v", acc.Mode)
	}
	return acc
}

func TestDecode_textStream(t *testing.T) {
	msgs := decodeAll(t, newDecoder(), lineMessageStart, lineTextStart, textDeltaLine("Hel"), textDeltaLine("lo"), lineBlockStop)

	if len(msgs) != 3 {
		t.Fatalf("got %d messages, want 3: %+v", len(msgs), msgs)
	}
	got := accumulate(t, msgs)
	if got.StreamID != "claude:output:msg_1:0" {
		t.Errorf("StreamID = %q", got.StreamID)
	}
	if out, _ := got.Content.(agent.Output); out.Text != "Hello" {
		t.Errorf("text = %q, want Hello", out.Text)
	}
}

func TestDecode_reasoningStream(t *testing.T) {
	msgs := decodeAll(t, newDecoder(), lineMessageStart, lineThinkStart, thinkingDeltaLine("plan"), lineBlockStop)

	got := accumulate(t, msgs)
	if r, _ := got.Content.(agent.Reasoning); r.Text != "plan" {
		t.Errorf("reasoning = %q, want plan", r.Text)
	}
}

func TestDecode_reasoningWithoutSummaryEmitsNothing(t *testing.T) {
	signature := `{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"abc"}}}`
	msgs := decodeAll(t, newDecoder(), lineMessageStart, lineThinkStart, thinkingDeltaLine(""), signature, lineBlockStop)

	if len(msgs) != 0 {
		t.Fatalf("got %d messages, want none: %+v", len(msgs), msgs)
	}
}

// bashResultTests use Bash result shapes recorded from Claude Code 2.1.301.
var bashResultTests = []struct {
	name          string
	content       string
	isError       bool
	toolUseResult string
	wantOutput    string
	wantExitCode  *int
}{
	{
		name:          "success with stdout and stderr",
		content:       `"hello world"`,
		toolUseResult: `{"stdout":"hello world","stderr":"warn","interrupted":false}`,
		wantOutput:    "hello world\nwarn",
		wantExitCode:  intPtr(0),
	},
	{
		name:          "success without output",
		content:       `"(Bash completed with no output)"`,
		toolUseResult: `{"stdout":"","stderr":"","interrupted":false}`,
		wantOutput:    "",
		wantExitCode:  intPtr(0),
	},
	{
		name:          "non-zero exit interpreted as success",
		content:       `"(Bash completed with no output)"`,
		toolUseResult: `{"stdout":"","stderr":"","interrupted":false,"returnCodeInterpretation":"No matches found"}`,
		wantOutput:    "[No matches found]",
	},
	{
		name:          "success without structured result",
		content:       `[{"type":"text","text":"hello"}]`,
		toolUseResult: `null`,
		wantOutput:    "hello",
	},
	{
		name:          "failure with exit code and output",
		content:       `"Exit code 3\nout\nerr"`,
		isError:       true,
		toolUseResult: `"Error: Exit code 3\nout\nerr"`,
		wantOutput:    "out\nerr",
		wantExitCode:  intPtr(3),
	},
	{
		name:          "failure with exit code only",
		content:       `"Exit code 1"`,
		isError:       true,
		toolUseResult: `"Error: Exit code 1"`,
		wantOutput:    "",
		wantExitCode:  intPtr(1),
	},
	{
		name:          "failure without exit code",
		content:       `"Reads outside the working directories are blocked."`,
		isError:       true,
		toolUseResult: `"Error: Reads outside the working directories are blocked."`,
		wantOutput:    "Reads outside the working directories are blocked.",
		wantExitCode:  intPtr(unknownExitCode),
	},
	{
		name:          "failure with unparsable exit code",
		content:       `"Exit code 99999999999999999999"`,
		isError:       true,
		toolUseResult: `"Error: Exit code 99999999999999999999"`,
		wantOutput:    "Exit code 99999999999999999999",
		wantExitCode:  intPtr(unknownExitCode),
	},
}

func TestDecode_bashCommand(t *testing.T) {
	for _, tt := range bashResultTests {
		t.Run(tt.name, func(t *testing.T) {
			msgs := decodeAll(t, newDecoder(),
				toolUseLine("toolu_1", "Bash", `{"command":"cat notes.txt","description":"show"}`),
				toolResultLine("toolu_1", tt.content, tt.isError, tt.toolUseResult),
			)

			got := accumulate(t, msgs)
			cmd, _ := got.Content.(agent.Command)
			if got.StreamID != "claude:command:toolu_1" {
				t.Errorf("StreamID = %q", got.StreamID)
			}
			if cmd.Command != "cat notes.txt" || cmd.Output != tt.wantOutput {
				t.Errorf("command = %+v, want output %q", cmd, tt.wantOutput)
			}
			if !equalIntPtr(cmd.ExitCode, tt.wantExitCode) {
				t.Errorf("ExitCode = %v, want %v", fmtIntPtr(cmd.ExitCode), fmtIntPtr(tt.wantExitCode))
			}
		})
	}
}

func equalIntPtr(a, b *int) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func fmtIntPtr(p *int) string {
	if p == nil {
		return "nil"
	}
	return strconv.Itoa(*p)
}

func TestDecode_writeCreatesFile(t *testing.T) {
	msgs := decodeAll(t, newDecoder(),
		toolUseLine("toolu_2", "Write", `{"file_path":"/w/notes.txt","content":"hello\n"}`),
		toolResultLine("toolu_2", `"File created successfully"`, false,
			`{"type":"create","filePath":"/w/notes.txt","content":"hello\n","structuredPatch":[],"originalFile":null}`),
	)

	set, _ := accumulate(t, msgs).Content.(agent.FileChangeSet)
	want := agent.FileChange{Path: "/w/notes.txt", Diff: "hello\n", ChangeKind: "add"}
	if set.Status != agent.ToolStatusCompleted || len(set.Changes) != 1 || set.Changes[0] != want {
		t.Errorf("file change set = %+v, want completed %+v", set, want)
	}
}

func TestDecode_editUpdatesFile(t *testing.T) {
	msgs := decodeAll(t, newDecoder(),
		toolUseLine("toolu_3", "Edit", `{"file_path":"/w/notes.txt","old_string":"hello","new_string":"hello world"}`),
		toolResultLine("toolu_3", `"The file has been updated"`, false,
			`{"filePath":"/w/notes.txt","oldString":"hello","newString":"hello world","originalFile":"hello\n",`+
				`"structuredPatch":[{"oldStart":1,"oldLines":1,"newStart":1,"newLines":1,"lines":["-hello","+hello world"]}]}`),
	)

	set, _ := accumulate(t, msgs).Content.(agent.FileChangeSet)
	want := agent.FileChange{Path: "/w/notes.txt", Diff: "@@ -1,1 +1,1 @@\n-hello\n+hello world\n", ChangeKind: "update"}
	if len(set.Changes) != 1 || set.Changes[0] != want {
		t.Errorf("changes = %+v, want %+v", set.Changes, want)
	}
}

func TestDecode_fileChangeStatus(t *testing.T) {
	tests := []struct {
		name    string
		denied  bool // coderoom denied the call
		content string
		want    agent.ToolStatus
	}{
		{"declined by coderoom", true, `"` + declineMessage + `"`, agent.ToolStatusDeclined},
		{"cancelled by coderoom", true, `"The user doesn't want to proceed with this tool use."`, agent.ToolStatusDeclined},
		{"denied by Claude", false, `"Claude requested permissions to write to /w/denied.txt, but you haven't granted it yet."`, agent.ToolStatusFailed},
		{"failed", false, `"File has not been read yet."`, agent.ToolStatusFailed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := newDecoder()
			if tt.denied {
				d.denied.add("toolu_4")
			}
			msgs := decodeAll(t, d,
				toolUseLine("toolu_4", "Write", `{"file_path":"/w/denied.txt","content":"no\n"}`),
				toolResultLine("toolu_4", tt.content, true, `"Error: denied"`),
			)
			set, _ := accumulate(t, msgs).Content.(agent.FileChangeSet)
			if set.Status != tt.want {
				t.Errorf("status = %q, want %q", set.Status, tt.want)
			}
			if len(set.Changes) != 1 || set.Changes[0].Path != "/w/denied.txt" {
				t.Errorf("changes = %+v, want the path only", set.Changes)
			}
		})
	}
}

func TestDecode_unmappedToolEmitsNothing(t *testing.T) {
	msgs := decodeAll(t, newDecoder(),
		toolUseLine("toolu_5", "Read", `{"file_path":"/w/notes.txt"}`),
		toolResultLine("toolu_5", `"hello"`, false, `{"type":"text"}`),
	)

	if len(msgs) != 0 {
		t.Fatalf("got %d messages, want none: %+v", len(msgs), msgs)
	}
}

func TestDecode_resultFlushesOpenStreams(t *testing.T) {
	d := newDecoder()
	decodeAll(t, d, lineMessageStart, lineTextStart, textDeltaLine("partial"),
		toolUseLine("toolu_6", "Bash", `{"command":"sleep 100"}`))

	out, err := d.decode([]byte(`{"type":"result","subtype":"error_during_execution","is_error":true,"terminal_reason":"aborted_streaming"}`))
	if err != nil {
		t.Fatal(err)
	}
	if out.signal != signalResult || !out.result.IsError {
		t.Errorf("signal = %v, result = %+v", out.signal, out.result)
	}
	want := []agent.Message{
		{StreamID: "claude:output:msg_1:0", Mode: agent.ModeFlush, Content: agent.Output{}},
		{StreamID: "claude:command:toolu_6", Mode: agent.ModeFlush, Content: agent.Command{}},
	}
	if len(out.msgs) != len(want) {
		t.Fatalf("got %+v, want %+v", out.msgs, want)
	}
	for i := range want {
		if out.msgs[i].StreamID != want[i].StreamID || out.msgs[i].Mode != want[i].Mode {
			t.Errorf("msg %d = %+v, want %+v", i, out.msgs[i], want[i])
		}
	}
}

func TestDecode_lifecycleAndIgnoredLines(t *testing.T) {
	tests := []struct {
		name   string
		line   string
		signal turnSignal
	}{
		{"system init", lineSystemInit, signalInit},
		{"result", lineResultOK, signalResult},
		{"replayed prompt", `{"type":"user","message":{"role":"user","content":"hi"},"isReplay":true}`, signalNone},
		{"permission denied with string message", `{"type":"system","subtype":"permission_denied","tool_name":"Write","message":"denied"}`, signalNone},
		{"rate limit", `{"type":"rate_limit_event"}`, signalNone},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := newDecoder().decode([]byte(tt.line))
			if err != nil {
				t.Fatal(err)
			}
			if out.signal != tt.signal || len(out.msgs) != 0 {
				t.Errorf("signal = %v, msgs = %+v; want signal %v and no messages", out.signal, out.msgs, tt.signal)
			}
		})
	}
}

func TestDecode_controlLines(t *testing.T) {
	d := newDecoder()
	req, err := d.decode([]byte(`{"type":"control_request","request_id":"r1","request":{"subtype":"can_use_tool","tool_name":"Bash"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if req.controlRequest == nil || req.controlRequest.RequestID != "r1" {
		t.Errorf("control request = %+v", req.controlRequest)
	}
	resp, err := d.decode([]byte(`{"type":"control_response","response":{"subtype":"success","request_id":"coderoom-init","response":{}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if resp.controlResponse == nil || resp.controlResponse.Response.RequestID != initRequestID {
		t.Errorf("control response = %+v", resp.controlResponse)
	}
}

func TestDecode_invalidJSON(t *testing.T) {
	if _, err := newDecoder().decode([]byte(`{not json`)); err == nil {
		t.Fatal("expected error")
	}
}
