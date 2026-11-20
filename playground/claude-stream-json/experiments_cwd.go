package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// e14Step is one turn: the Bash calls it asks for, in order.
type e14Step struct {
	commands []string
	parallel bool // ask for all calls in one assistant message
}

// e14Steps: every command writes a file so that it needs a permission
// prompt; pwd prints the directory the shell ran in. Turn 6 makes two calls,
// so the second one starts from a cwd the turn's init event cannot know.
// Turns 7 and 8 ask for two calls at once while one of them changes
// directory; in turn 8 the first call does, so a hook run for the second
// call before the first finished would report a stale cwd.
var e14Steps = []e14Step{
	{commands: []string{"pwd && touch m1"}},
	{commands: []string{"cd sub && pwd && touch m2"}},
	{commands: []string{"pwd && touch m3"}},
	{commands: []string{"cd /tmp && pwd"}},
	{commands: []string{"pwd && touch m5"}},
	{commands: []string{"cd sub && pwd && touch m6", "pwd && touch m7"}},
	{commands: []string{"pwd && touch p1", "cd .. && pwd && touch p2"}, parallel: true},
	{commands: []string{"cd sub && pwd && touch p3", "pwd && touch p4"}, parallel: true},
}

// e14HookDelay is how long the hook sleeps before logging, in seconds. If
// the entry is logged when can_use_tool arrives, Claude waited for the hook.
const e14HookDelay = 1

// e14Call is what one Bash tool call exposed on each channel. The seq
// fields order prompts and results across the whole session.
type e14Call struct {
	Command          string   `json:"command"`
	MessageID        string   `json:"message_id"`
	PromptSeen       bool     `json:"prompt_seen"`
	HookBeforePrompt bool     `json:"hook_logged_before_prompt"`
	PromptSeq        int      `json:"prompt_seq"`
	ResultSeq        int      `json:"result_seq"`
	BlockedPath      string   `json:"blocked_path,omitempty"`
	SuggestDirs      []string `json:"suggested_directories,omitempty"`
	HookCwd          string   `json:"hook_cwd,omitempty"`
	InitCwd          string   `json:"init_cwd,omitempty"`
	Output           string   `json:"output"`
}

// e14Run is the state of one E14 session.
type e14Run struct {
	s       *session
	hookLog string
	calls   map[string]*e14Call
	order   []string
	seq     int
	// inband: hook cwd by tool_use_id from hook_callback requests (E15).
	// Nil when the hook is a command hook writing hookLog (E14).
	inband map[string]string
}

// e15CallbackID is the hook callback id registered in initialize.
const e15CallbackID = "coderoom-bash-cwd"

// E14: does any channel report the cwd a Bash call runs in, after earlier
// calls changed it? Compares the PreToolUse hook cwd, the can_use_tool
// fields and the per-turn init cwd with the shell's own pwd.
func e14BashCwd() error {
	hookLog, settings, err := e14HookSettings()
	if err != nil {
		return err
	}
	return runBashCwd("e14", &e14Run{hookLog: hookLog}, "--settings", settings)
}

// E15: E14 with the hook registered in initialize and answered over the
// protocol (hook_callback), as the Agent SDK does. Each hook is answered
// with {} after the same delay as the E14 command hook.
func e15BashCwdInband() error {
	return runBashCwd("e15", &e14Run{inband: map[string]string{}})
}

func runBashCwd(name string, r *e14Run, extra ...string) error {
	dir, err := workdir(name)
	if err != nil {
		return err
	}
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		return err
	}
	s, err := start(startOpts{fixture: name + "-bash-cwd", args: commonArgs(extra...), dir: dir})
	if err != nil {
		return err
	}
	defer s.finish()
	r.s, r.calls = s, map[string]*e14Call{}
	if err := r.initialize(); err != nil {
		return err
	}
	s.observe("process_dir", dir)

	for _, step := range e14Steps {
		if err := r.turn(e14Prompt(step)); err != nil {
			return err
		}
	}
	hooks, err := r.hookCwds()
	if err != nil {
		return err
	}
	for i, id := range r.order {
		c := r.calls[id]
		c.HookCwd = hooks[id]
		s.observe(fmt.Sprintf("call_%d", i+1), c)
	}
	return nil
}

func (r *e14Run) initialize() error {
	if r.inband == nil {
		_, err := r.s.initialize()
		return err
	}
	hooks := map[string]any{"PreToolUse": []any{
		map[string]any{"matcher": "Bash", "hookCallbackIds": []string{e15CallbackID}},
	}}
	id, err := r.s.control(map[string]any{"subtype": "initialize", "hooks": hooks})
	if err != nil {
		return err
	}
	resp, err := r.s.until(60*time.Second, nil, isControlResponse(id))
	if err != nil {
		return err
	}
	body, _ := resp.Body["response"].(map[string]any)
	r.s.observe("initialize_response_subtype", body["subtype"])
	return nil
}

// hookCwds returns the hook cwd recorded for each tool_use_id so far.
func (r *e14Run) hookCwds() (map[string]string, error) {
	if r.inband != nil {
		return r.inband, nil
	}
	hooks, err := readHookLog(r.hookLog)
	if os.IsNotExist(err) {
		return map[string]string{}, nil
	}
	return hooks, err
}

// answerHook records a hook_callback and answers it with an empty output,
// which lets the call continue unchanged.
func (r *e14Run) answerHook(m msg) error {
	reqID := str(m.Body["request_id"])
	req, _ := m.Body["request"].(map[string]any)
	input, _ := req["input"].(map[string]any)
	if req["callback_id"] != e15CallbackID {
		r.s.observe("unexpected_hook_callback", req)
	}
	id := str(req["tool_use_id"])
	if id != str(input["tool_use_id"]) {
		r.s.observe("hook_tool_use_id_mismatch", req)
	}
	time.Sleep(e14HookDelay * time.Second)
	r.inband[id] = str(input["cwd"])
	return r.s.send(map[string]any{
		"type":     "control_response",
		"response": map[string]any{"subtype": "success", "request_id": reqID, "response": map[string]any{}},
	})
}

func isHookCallback(m msg) bool {
	req, _ := m.Body["request"].(map[string]any)
	return m.Type == "control_request" && req["subtype"] == "hook_callback"
}

func e14Prompt(step e14Step) string {
	if len(step.commands) == 1 {
		return "Run exactly this command with the Bash tool, once, unchanged: `" + step.commands[0] + "`. Then reply with only its output."
	}
	quoted := make([]string, len(step.commands))
	for i, c := range step.commands {
		quoted[i] = "`" + c + "`"
	}
	if step.parallel {
		return "Run these commands unchanged as separate Bash tool calls, all in parallel in a single message: " +
			strings.Join(quoted, " and ") + ". Then reply with only their outputs."
	}
	return "Run these commands unchanged as separate Bash tool calls, one after another, never in parallel: " +
		strings.Join(quoted, ", then ") + ". Then reply with only their outputs."
}

// e14HookSettings writes a PreToolUse hook that appends its stdin to a log.
func e14HookSettings() (logPath, settings string, err error) {
	hookDir, err := os.MkdirTemp("", "claude-stream-json-e14-hook-")
	if err != nil {
		return "", "", err
	}
	logPath = filepath.Join(hookDir, "pretooluse.jsonl")
	command := fmt.Sprintf("in=$(cat); sleep %d; printf '%%s\\n' \"$in\" >> %q", e14HookDelay, logPath)
	b, err := json.Marshal(map[string]any{"hooks": map[string]any{"PreToolUse": []any{
		map[string]any{"matcher": "Bash", "hooks": []any{map[string]any{"type": "command", "command": command}}},
	}}})
	return logPath, string(b), err
}

// turn runs one turn, allowing every prompt, and records each Bash call.
func (r *e14Run) turn(prompt string) error {
	logf("  > %s", prompt)
	if err := r.s.user(prompt); err != nil {
		return err
	}
	var initCwd string
	var perr error
	_, err := r.s.until(turnTimeout, func(m msg) {
		switch {
		case m.Type == "system" && m.Subtype == "init":
			initCwd = str(m.Body["cwd"])
		case m.Type == "assistant":
			message, _ := m.Body["message"].(map[string]any)
			for _, u := range contentBlocks(m, "tool_use") {
				if u["name"] == "Bash" {
					in, _ := u["input"].(map[string]any)
					c := r.call(str(u["id"]), initCwd)
					c.Command, c.MessageID = str(in["command"]), str(message["id"])
				}
			}
		case m.Type == "user":
			for _, res := range contentBlocks(m, "tool_result") {
				if c := r.calls[str(res["tool_use_id"])]; c != nil {
					c.Output = toolResultText(res["content"])
					c.ResultSeq = r.next()
				}
			}
		case isPermissionRequest(m):
			perr = r.prompt(m, initCwd)
		case isHookCallback(m) && r.inband != nil:
			perr = r.answerHook(m)
		case m.Type == "control_request":
			perr = r.s.rejectControl(m)
		}
	}, func(m msg) bool { return m.Type == "result" })
	if perr != nil {
		return perr
	}
	return err
}

// prompt records what can_use_tool carried and whether the hook had
// already logged the call, then allows it.
func (r *e14Run) prompt(m msg, initCwd string) error {
	req, _ := m.Body["request"].(map[string]any)
	id := str(req["tool_use_id"])
	hooks, err := r.hookCwds()
	if err != nil {
		return err
	}
	c := r.call(id, initCwd)
	_, c.HookBeforePrompt = hooks[id]
	c.PromptSeen, c.PromptSeq = true, r.next()
	c.BlockedPath = str(req["blocked_path"])
	c.SuggestDirs = suggestedDirectories(req["permission_suggestions"])
	return r.s.answerPermission(m, allowAll)
}

func (r *e14Run) call(id, initCwd string) *e14Call {
	if r.calls[id] == nil {
		r.calls[id] = &e14Call{InitCwd: initCwd}
		r.order = append(r.order, id)
	}
	return r.calls[id]
}

func (r *e14Run) next() int { r.seq++; return r.seq }

func contentBlocks(m msg, kind string) []map[string]any {
	message, _ := m.Body["message"].(map[string]any)
	content, _ := message["content"].([]any)
	var out []map[string]any
	for _, c := range content {
		if b, ok := c.(map[string]any); ok && b["type"] == kind {
			out = append(out, b)
		}
	}
	return out
}

func toolResultText(v any) string {
	if s, ok := v.(string); ok {
		return strings.TrimSpace(s)
	}
	parts, _ := v.([]any)
	var out []string
	for _, p := range parts {
		if b, ok := p.(map[string]any); ok {
			out = append(out, str(b["text"]))
		}
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

func suggestedDirectories(v any) []string {
	suggestions, _ := v.([]any)
	var out []string
	for _, sg := range suggestions {
		b, _ := sg.(map[string]any)
		dirs, _ := b["directories"].([]any)
		for _, d := range dirs {
			out = append(out, str(d))
		}
	}
	return out
}

// readHookLog maps tool_use_id to the cwd the PreToolUse hook received.
func readHookLog(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	out := map[string]string{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		var in struct {
			ToolUseID string `json:"tool_use_id"`
			Cwd       string `json:"cwd"`
		}
		if json.Unmarshal(sc.Bytes(), &in) == nil && in.ToolUseID != "" {
			out[in.ToolUseID] = in.Cwd
		}
	}
	return out, sc.Err()
}
