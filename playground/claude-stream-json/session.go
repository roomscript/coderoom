package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

// baseArgs is the long-running stream-json invocation under test.
var baseArgs = []string{
	"--print", "--verbose",
	"--input-format", "stream-json",
	"--output-format", "stream-json",
	"--replay-user-messages",
	"--include-partial-messages",
	"--permission-prompts", "host",
	// The Agent SDK adds this whenever a can_use_tool callback is set.
	// Without it, prompts are denied without reaching the host.
	"--permission-prompt-tool", "stdio",
}

// strippedEnvPrefixes are variables a parent Claude Code session exports.
// A child that inherits them may behave as a nested session.
var strippedEnvPrefixes = []string{"CLAUDECODE=", "CLAUDE_CODE_", "CLAUDE_PID=", "CLAUDE_EFFORT="}

// msg is one stdout line from Claude.
type msg struct {
	Raw     json.RawMessage
	Type    string
	Subtype string
	Body    map[string]any
}

type startOpts struct {
	fixture string   // fixture file name without extension
	args    []string // extra CLI args appended to baseArgs
	dir     string   // working directory of the process
	env     []string // extra env entries (KEY=VALUE), applied after stripping
	keepEnv bool     // do not strip parent Claude variables
}

// session drives one claude process and records every line to a fixture.
type session struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	lines   chan msg
	exited  chan struct{}
	exitErr error
	rec     *recorder
	reqID   int
}

func start(o startOpts) (*session, error) {
	args := append(append([]string{}, baseArgs...), o.args...)
	env := childEnv(o.keepEnv, o.env)

	rec, err := newRecorder(o.fixture, map[string]any{
		"claude_version": claudeVersion(),
		"args":           args,
		"dir":            o.dir,
		"env_added":      o.env,
		"env_stripped":   !o.keepEnv,
		"started":        time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		return nil, err
	}

	cmd := exec.Command("claude", args...)
	cmd.Dir = o.dir
	cmd.Env = env
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	logf("[%s] started pid %d", o.fixture, cmd.Process.Pid)

	s := &session{cmd: cmd, stdin: stdin, lines: make(chan msg, 1024), exited: make(chan struct{}), rec: rec}

	var readers sync.WaitGroup
	readers.Add(2)
	go func() {
		defer readers.Done()
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 1<<20), 64<<20)
		for sc.Scan() {
			line := append([]byte{}, sc.Bytes()...)
			rec.write("out", line)
			m := msg{Raw: line}
			if json.Unmarshal(line, &m.Body) == nil {
				m.Type, _ = m.Body["type"].(string)
				m.Subtype, _ = m.Body["subtype"].(string)
			}
			s.lines <- m
		}
	}()
	go func() {
		defer readers.Done()
		sc := bufio.NewScanner(stderr)
		for sc.Scan() {
			rec.write("stderr", sc.Bytes())
		}
	}()
	go func() {
		readers.Wait()
		s.exitErr = cmd.Wait()
		rec.note("exit", map[string]any{"error": fmt.Sprint(s.exitErr), "code": cmd.ProcessState.ExitCode()})
		close(s.lines)
		close(s.exited)
	}()
	return s, nil
}

func childEnv(keep bool, extra []string) []string {
	var env []string
	for _, kv := range os.Environ() {
		if !keep && hasAnyPrefix(kv, strippedEnvPrefixes) {
			continue
		}
		env = append(env, kv)
	}
	return append(env, extra...)
}

func hasAnyPrefix(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

func (s *session) send(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	s.rec.write("in", b)
	_, err = fmt.Fprintf(s.stdin, "%s\n", b)
	return err
}

func (s *session) user(text string) error {
	return s.send(map[string]any{
		"type":               "user",
		"message":            map[string]any{"role": "user", "content": text},
		"parent_tool_use_id": nil,
		"session_id":         "default",
	})
}

// control sends a control_request and returns its request_id.
func (s *session) control(req map[string]any) (string, error) {
	s.reqID++
	id := fmt.Sprintf("req_%d", s.reqID)
	return id, s.send(map[string]any{"type": "control_request", "request_id": id, "request": req})
}

// next returns the next stdout message, or ok=false on timeout or exit.
func (s *session) next(timeout time.Duration) (msg, bool) {
	select {
	case m, ok := <-s.lines:
		return m, ok
	case <-time.After(timeout):
		return msg{}, false
	}
}

// until reads messages until match returns true. Every message read is
// passed to each before match is evaluated.
func (s *session) until(timeout time.Duration, each func(msg), match func(msg) bool) (msg, error) {
	deadline := time.Now().Add(timeout)
	for {
		left := time.Until(deadline)
		if left <= 0 {
			return msg{}, fmt.Errorf("timeout after %s", timeout)
		}
		m, ok := s.next(left)
		if !ok {
			select {
			case <-s.exited:
				return msg{}, fmt.Errorf("process exited: %v", s.exitErr)
			default:
				return msg{}, fmt.Errorf("timeout after %s", timeout)
			}
		}
		if each != nil {
			each(m)
		}
		if match(m) {
			return m, nil
		}
	}
}

// initialize performs the SDK control handshake.
func (s *session) initialize() (msg, error) {
	id, err := s.control(map[string]any{"subtype": "initialize", "hooks": nil})
	if err != nil {
		return msg{}, err
	}
	return s.until(60*time.Second, nil, isControlResponse(id))
}

func isControlResponse(id string) func(msg) bool {
	return func(m msg) bool {
		if m.Type != "control_response" {
			return false
		}
		r, _ := m.Body["response"].(map[string]any)
		return r["request_id"] == id
	}
}

// decision answers a can_use_tool request. A nil input on allow sends the
// original tool input back unchanged.
type decision struct {
	allow   bool
	message string         // deny message shown to the model
	input   map[string]any // updatedInput on allow
}

// permissionPolicy decides a can_use_tool request.
type permissionPolicy func(tool string, input map[string]any) decision

func allowAll(string, map[string]any) decision { return decision{allow: true} }

func denyAll(string, map[string]any) decision {
	return decision{message: "denied by playground"}
}

// answerPermission replies to a can_use_tool control request.
func (s *session) answerPermission(m msg, policy permissionPolicy) error {
	reqID, _ := m.Body["request_id"].(string)
	req, _ := m.Body["request"].(map[string]any)
	tool, _ := req["tool_name"].(string)
	input, _ := req["input"].(map[string]any)
	d := policy(tool, input)
	logf("  permission %s %v -> allow=%v", tool, compact(input), d.allow)
	resp := map[string]any{"behavior": "deny", "message": d.message}
	if d.allow {
		if d.input == nil {
			d.input = input
		}
		resp = map[string]any{"behavior": "allow", "updatedInput": d.input}
	}
	return s.send(map[string]any{
		"type": "control_response",
		"response": map[string]any{
			"subtype":    "success",
			"request_id": reqID,
			"response":   resp,
		},
	})
}

// rejectControl answers a control request the harness does not handle,
// the way the Agent SDK does for unsupported subtypes.
func (s *session) rejectControl(m msg) error {
	reqID, _ := m.Body["request_id"].(string)
	req, _ := m.Body["request"].(map[string]any)
	s.observe("unhandled_control_request", req)
	return s.send(map[string]any{
		"type": "control_response",
		"response": map[string]any{
			"subtype":    "error",
			"request_id": reqID,
			"error":      fmt.Sprintf("unsupported control request subtype: %v", req["subtype"]),
		},
	})
}

// turnResult summarises one turn.
type turnResult struct {
	result      msg
	text        string   // concatenated text deltas
	permissions int      // can_use_tool requests seen
	tools       []string // tool_use names in assistant messages
}

// turn sends a user message and reads until its result event, answering
// permission requests with policy (nil denies everything).
func (s *session) turn(text string, timeout time.Duration, policy permissionPolicy) (turnResult, error) {
	logf("  > %s", text)
	var tr turnResult
	if policy == nil {
		policy = denyAll
	}
	if err := s.user(text); err != nil {
		return tr, err
	}
	var perr error
	res, err := s.until(timeout, func(m msg) {
		tr.text += textDelta(m)
		tr.tools = append(tr.tools, toolUses(m)...)
		if err := s.handleControl(m, policy, &tr.permissions); err != nil {
			perr = err
		}
	}, func(m msg) bool { return m.Type == "result" })
	if perr != nil {
		return tr, perr
	}
	tr.result = res
	if err == nil {
		logf("  < %q (%s) tools=%v", strings.TrimSpace(tr.text), res.Subtype, tr.tools)
	}
	return tr, err
}

// handleControl answers any control request from the CLI.
func (s *session) handleControl(m msg, policy permissionPolicy, permissions *int) error {
	if m.Type != "control_request" {
		return nil
	}
	if isPermissionRequest(m) {
		*permissions++
		return s.answerPermission(m, policy)
	}
	return s.rejectControl(m)
}

// toolUses returns the tool names in a complete assistant message.
func toolUses(m msg) []string {
	if m.Type != "assistant" {
		return nil
	}
	inner, _ := m.Body["message"].(map[string]any)
	blocks, _ := inner["content"].([]any)
	var names []string
	for _, b := range blocks {
		bm, _ := b.(map[string]any)
		if bm["type"] == "tool_use" {
			names = append(names, str(bm["name"]))
		}
	}
	return names
}

func isPermissionRequest(m msg) bool {
	if m.Type != "control_request" {
		return false
	}
	req, _ := m.Body["request"].(map[string]any)
	return req["subtype"] == "can_use_tool"
}

// textDelta extracts text from a stream_event content_block_delta.
func textDelta(m msg) string {
	if m.Type != "stream_event" {
		return ""
	}
	ev, _ := m.Body["event"].(map[string]any)
	if ev["type"] != "content_block_delta" {
		return ""
	}
	d, _ := ev["delta"].(map[string]any)
	if d["type"] != "text_delta" {
		return ""
	}
	t, _ := d["text"].(string)
	return t
}

// closeAndWait closes stdin and waits for the process to exit. If it does
// not exit within grace, it sends SIGTERM and then SIGKILL.
func (s *session) closeAndWait(grace time.Duration) (exitedCleanly bool, took time.Duration) {
	t0 := time.Now()
	s.rec.note("close_stdin", nil)
	s.stdin.Close()
	if s.waitExit(grace) {
		return true, time.Since(t0)
	}
	s.terminate(grace)
	return false, time.Since(t0)
}

func (s *session) waitExit(d time.Duration) bool {
	// Drain stdout so the reader goroutine never blocks on a full channel.
	timeout := time.After(d)
	for {
		select {
		case _, ok := <-s.lines:
			if !ok {
				<-s.exited
				return true
			}
		case <-s.exited:
			return true
		case <-timeout:
			return false
		}
	}
}

func (s *session) terminate(grace time.Duration) bool {
	s.rec.note("sigterm", nil)
	s.cmd.Process.Signal(syscall.SIGTERM)
	if s.waitExit(grace) {
		return true
	}
	s.rec.note("sigkill", nil)
	s.cmd.Process.Kill()
	s.waitExit(5 * time.Second)
	return false
}

func (s *session) finish() {
	select {
	case <-s.exited:
	default:
		s.closeAndWait(10 * time.Second)
	}
	s.rec.close()
}

// recorder writes timestamped JSON lines: one meta header, then one entry
// per line in or out.
type recorder struct {
	mu sync.Mutex
	f  *os.File
	t0 time.Time
}

func newRecorder(name string, meta map[string]any) (*recorder, error) {
	if err := os.MkdirAll(fixturesDir, 0o755); err != nil {
		return nil, err
	}
	f, err := os.Create(filepath.Join(fixturesDir, name+".jsonl"))
	if err != nil {
		return nil, err
	}
	r := &recorder{f: f, t0: time.Now()}
	r.emit(map[string]any{"meta": meta})
	return r, nil
}

func (r *recorder) write(dir string, line []byte) {
	entry := map[string]any{"t_ms": time.Since(r.t0).Milliseconds(), "dir": dir}
	if json.Valid(line) {
		entry["json"] = json.RawMessage(line)
	} else {
		entry["text"] = string(line)
	}
	r.emit(entry)
}

// note records a harness event (close, signal, exit, observation).
func (r *recorder) note(event string, data any) {
	r.emit(map[string]any{"t_ms": time.Since(r.t0).Milliseconds(), "dir": "note", "event": event, "data": data})
}

func (r *recorder) emit(entry map[string]any) {
	b, _ := json.Marshal(entry)
	b = redact(b)
	r.mu.Lock()
	defer r.mu.Unlock()
	fmt.Fprintf(r.f, "%s\n", b)
}

func (r *recorder) close() { r.f.Close() }

func claudeVersion() string {
	out, err := exec.Command("claude", "--version").Output()
	if err != nil {
		return "unknown: " + err.Error()
	}
	return strings.TrimSpace(string(out))
}

func compact(v any) string {
	b, _ := json.Marshal(v)
	if len(b) > 160 {
		return string(b[:160]) + "…"
	}
	return string(b)
}

func logf(format string, a ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", a...)
}
