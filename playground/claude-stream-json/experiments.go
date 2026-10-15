package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const turnTimeout = 120 * time.Second

// observe logs an observation and records it in the fixture.
func (s *session) observe(key string, value any) {
	logf("  observe %s = %s", key, compact(value))
	s.rec.note("observe", map[string]any{key: value})
}

// E1: start without sending anything. Does the process stay alive and wait
// for stdin? What does the init event contain, and when is it emitted?
func e1Idle() error {
	dir, err := workdir("e1")
	if err != nil {
		return err
	}
	s, err := start(startOpts{fixture: "e1-idle", args: commonArgs(), dir: dir})
	if err != nil {
		return err
	}
	defer s.finish()

	m, ok := s.next(10 * time.Second)
	s.observe("output_before_any_input", ok)
	if ok {
		s.observe("first_unprompted_type", m.Type)
	}
	select {
	case <-s.exited:
		return fmt.Errorf("process exited while idle: %v", s.exitErr)
	default:
		s.observe("alive_after_10s_idle", true)
	}

	resp, err := s.initialize()
	if err != nil {
		return fmt.Errorf("initialize: %w", err)
	}
	s.observe("initialize_response_keys", keys(resp.Body["response"]))

	init, ok := s.next(5 * time.Second)
	s.observe("init_emitted_after_initialize_without_user_message", ok && init.Subtype == "init")

	tr, err := s.turn("Reply with just: ok", turnTimeout, nil)
	if err != nil {
		return err
	}
	s.observe("turn_result_subtype", tr.result.Subtype)

	clean, took := s.closeAndWait(15 * time.Second)
	s.observe("exit_on_stdin_close", map[string]any{"clean": clean, "took_ms": took.Milliseconds()})
	return nil
}

// E2: two turns in one process. Is context kept? How does a turn end?
func e2TwoTurns() error {
	dir, err := workdir("e2")
	if err != nil {
		return err
	}
	s, err := start(startOpts{fixture: "e2-two-turns", args: commonArgs(), dir: dir})
	if err != nil {
		return err
	}
	defer s.finish()
	if _, err := s.initialize(); err != nil {
		return err
	}

	if _, err := s.turn("Remember the number 7. Reply with just: ok", turnTimeout, nil); err != nil {
		return err
	}
	tr, err := s.turn("What number did I give you? Reply with just the number.", turnTimeout, nil)
	if err != nil {
		return err
	}
	s.observe("context_kept", contains(tr.text, "7"))
	s.observe("second_result_session_id", tr.result.Body["session_id"])
	return nil
}

// E3: a tool call that needs approval; allow once, deny once, then check
// the session is still usable.
func e3Permissions() error {
	dir, err := workdir("e3")
	if err != nil {
		return err
	}
	s, err := start(startOpts{fixture: "e3-permissions", args: commonArgs(), dir: dir})
	if err != nil {
		return err
	}
	defer s.finish()
	if _, err := s.initialize(); err != nil {
		return err
	}

	tr, err := s.turn("Create a file named allowed.txt in the current directory containing the word yes. Use the Write tool. Do not ask questions.", turnTimeout, allowAll)
	if err != nil {
		return err
	}
	s.observe("allow_permission_requests", tr.permissions)
	s.observe("allowed_file_exists", fileExists(filepath.Join(dir, "allowed.txt")))

	deny := func(string, map[string]any) decision {
		return decision{message: "The coderoom user denied this action."}
	}
	tr, err = s.turn("Create a file named denied.txt in the current directory containing the word no. Use the Write tool. Do not ask questions.", turnTimeout, deny)
	if err != nil {
		return err
	}
	s.observe("deny_permission_requests", tr.permissions)
	s.observe("denied_file_exists", fileExists(filepath.Join(dir, "denied.txt")))
	s.observe("deny_result_permission_denials", tr.result.Body["permission_denials"])

	tr, err = s.turn("Reply with just: still here", turnTimeout, nil)
	if err != nil {
		return err
	}
	s.observe("usable_after_deny", contains(tr.text, "still here"))
	return nil
}

// E4: interrupt a long turn with the control protocol, then send another.
func e4Interrupt() error {
	dir, err := workdir("e4")
	if err != nil {
		return err
	}
	s, err := start(startOpts{fixture: "e4-interrupt", args: commonArgs(), dir: dir})
	if err != nil {
		return err
	}
	defer s.finish()
	if _, err := s.initialize(); err != nil {
		return err
	}

	if err := s.user("Count from 1 to 400, one number per line, with no other text."); err != nil {
		return err
	}
	var text string
	_, err = s.until(turnTimeout, func(m msg) { text += textDelta(m) }, func(m msg) bool {
		return strings.Count(text, "\n") >= 20 || m.Type == "result"
	})
	if err != nil {
		return fmt.Errorf("waiting for output: %w", err)
	}
	s.observe("lines_before_interrupt", strings.Count(text, "\n"))

	t0 := time.Now()
	id, err := s.control(map[string]any{"subtype": "interrupt"})
	if err != nil {
		return err
	}
	var gotResp bool
	res, err := s.until(30*time.Second, func(m msg) {
		if isControlResponse(id)(m) {
			gotResp = true
			s.observe("interrupt_response_ms", time.Since(t0).Milliseconds())
		}
	}, func(m msg) bool { return m.Type == "result" })
	if err != nil {
		return fmt.Errorf("waiting for result after interrupt: %w", err)
	}
	s.observe("interrupt_control_response", gotResp)
	s.observe("interrupted_result", map[string]any{
		"subtype":   res.Subtype,
		"terminal":  res.Body["terminal_reason"],
		"stop":      res.Body["stop_reason"],
		"is_error":  res.Body["is_error"],
		"result_ms": time.Since(t0).Milliseconds(),
	})
	// Late control response, if it comes after the result.
	if !gotResp {
		if _, err := s.until(5*time.Second, nil, isControlResponse(id)); err == nil {
			s.observe("interrupt_control_response_after_result", true)
		}
	}

	tr, err := s.turn("Reply with just: ok", turnTimeout, nil)
	if err != nil {
		return fmt.Errorf("turn after interrupt: %w", err)
	}
	s.observe("usable_after_interrupt", contains(tr.text, "ok"))
	return nil
}

// E5: one turn that exercises reasoning, Bash, Write and Edit with partial
// messages on. The fixture is the deliverable; the observations count types.
//
// The second variant passes --thinking-display summarized (hidden flag the
// Agent SDK sets) to see whether reasoning text is streamed.
func e5Partials() error {
	for _, v := range []struct {
		fixture string
		extra   []string
	}{
		{"e5-partials", nil},
		{"e5-partials-thinking-summarized", []string{"--thinking-display", "summarized"}},
	} {
		if err := e5Run(v.fixture, v.extra); err != nil {
			return fmt.Errorf("%s: %w", v.fixture, err)
		}
	}
	return nil
}

func e5Run(fixture string, extra []string) error {
	dir, err := workdir("e5")
	if err != nil {
		return err
	}
	s, err := start(startOpts{fixture: fixture, args: commonArgs(append([]string{"--effort", "high"}, extra...)...), dir: dir})
	if err != nil {
		return err
	}
	defer s.finish()
	if _, err := s.initialize(); err != nil {
		return err
	}

	counts := map[string]int{}
	if err := s.user("Think briefly about the task first. Then: 1) run `ls -la` with the Bash tool; 2) create notes.txt containing hello with the Write tool; 3) change it to hello world with the Edit tool; 4) run `cat notes.txt` with Bash. Finish with a one-sentence summary. Do not ask questions."); err != nil {
		return err
	}
	var thinking string
	_, err = s.until(turnTimeout, func(m msg) {
		counts[eventKey(m)]++
		thinking += thinkingDelta(m)
		if isPermissionRequest(m) {
			s.answerPermission(m, allowAll)
		}
	}, func(m msg) bool { return m.Type == "result" })
	if err != nil {
		return err
	}
	s.observe("event_counts", counts)
	s.observe("thinking_text_chars", len(thinking))
	b, _ := os.ReadFile(filepath.Join(dir, "notes.txt"))
	s.observe("notes_txt", strings.TrimSpace(string(b)))
	return nil
}

func thinkingDelta(m msg) string {
	ev, _ := m.Body["event"].(map[string]any)
	d, _ := ev["delta"].(map[string]any)
	if d["type"] != "thinking_delta" {
		return ""
	}
	return str(d["thinking"])
}

// eventKey names a message by type, subtype and, for stream events, the
// inner event and delta or block type.
func eventKey(m msg) string {
	k := m.Type
	if m.Subtype != "" {
		k += "/" + m.Subtype
	}
	switch m.Type {
	case "stream_event":
		ev, _ := m.Body["event"].(map[string]any)
		k += "/" + str(ev["type"])
		if d, ok := ev["delta"].(map[string]any); ok {
			k += "/" + str(d["type"])
		}
		if b, ok := ev["content_block"].(map[string]any); ok {
			k += "/" + str(b["type"])
		}
	case "control_request":
		r, _ := m.Body["request"].(map[string]any)
		k += "/" + str(r["subtype"])
	case "assistant", "user":
		inner, _ := m.Body["message"].(map[string]any)
		if blocks, ok := inner["content"].([]any); ok {
			var types []string
			for _, b := range blocks {
				bm, _ := b.(map[string]any)
				types = append(types, str(bm["type"]))
			}
			k += "/" + strings.Join(types, "+")
		}
	}
	return k
}

// E6: the Bash tool's cwd when started from a non-shell parent (this Go
// program) whose PWD differs from the process directory.
func e6Cwd() error {
	for _, variant := range []struct {
		name string
		env  func(dir string) []string
	}{
		{"inherited-pwd", func(string) []string { return nil }},
		{"pwd-set", func(dir string) []string { return []string{"PWD=" + dir} }},
	} {
		dir, err := workdir("e6")
		if err != nil {
			return err
		}
		if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
			return err
		}
		s, err := start(startOpts{fixture: "e6-cwd-" + variant.name, args: commonArgs(), dir: dir, env: variant.env(dir)})
		if err != nil {
			return err
		}
		parentPWD := os.Getenv("PWD")
		s.observe("parent_pwd", parentPWD)
		s.observe("process_dir", dir)
		if _, err := s.initialize(); err != nil {
			s.finish()
			return err
		}
		tr, err := s.turn("Run exactly `pwd` with the Bash tool, then `cd sub` with the Bash tool. Reply with only the output of pwd.", turnTimeout, allowAll)
		if err != nil {
			s.finish()
			return err
		}
		s.observe("bash_pwd_reply", strings.TrimSpace(tr.text))
		s.observe("bash_pwd_is_process_dir", contains(tr.text, dir))
		tr, err = s.turn("Run exactly `pwd` with the Bash tool again. Reply with only its output.", turnTimeout, allowAll)
		if err != nil {
			s.finish()
			return err
		}
		s.observe("bash_pwd_after_cd_sub", strings.TrimSpace(tr.text))
		s.finish()
	}
	return nil
}

// E7: --json-schema. Is structured output produced per turn? Does a free
// chat turn still work in the same process?
func e7JSONSchema() error {
	dir, err := workdir("e7")
	if err != nil {
		return err
	}
	schema := `{"type":"object","properties":{"verdict":{"type":"string","enum":["pass","fail"]},"reason":{"type":"string"}},"required":["verdict","reason"],"additionalProperties":false}`
	s, err := start(startOpts{fixture: "e7-json-schema", args: commonArgs("--json-schema", schema), dir: dir})
	if err != nil {
		return err
	}
	defer s.finish()
	if _, err := s.initialize(); err != nil {
		return err
	}

	for i, prompt := range []string{
		"Judge the claim: 2 + 2 = 4.",
		"Judge the claim: 2 + 2 = 5.",
		"Say hello in one word.",
	} {
		tr, err := s.turn(prompt, turnTimeout, nil)
		if err != nil {
			return err
		}
		s.observe(fmt.Sprintf("turn%d_structured_output", i+1), tr.result.Body["structured_output"])
		s.observe(fmt.Sprintf("turn%d_result_text", i+1), tr.result.Body["result"])
	}
	return nil
}

// E8: stop by closing stdin, and by SIGTERM while idle; resume both
// sessions in new processes.
func e8StopResume() error {
	dir, err := workdir("e8")
	if err != nil {
		return err
	}
	stops := []struct {
		name, word string
		stop       func(*session) map[string]any
	}{
		{"close-stdin", "pineapple", func(s *session) map[string]any {
			clean, took := s.closeAndWait(15 * time.Second)
			return map[string]any{"clean": clean, "took_ms": took.Milliseconds(), "code": exitCode(s)}
		}},
		{"sigterm", "mango", func(s *session) map[string]any {
			t0 := time.Now()
			s.rec.note("sigterm", nil)
			s.cmd.Process.Signal(syscall.SIGTERM)
			clean := s.waitExit(15 * time.Second)
			return map[string]any{"exited": clean, "took_ms": time.Since(t0).Milliseconds(), "code": exitCode(s)}
		}},
	}
	for _, st := range stops {
		s, err := start(startOpts{fixture: "e8-stop-" + st.name, args: commonArgs(), dir: dir})
		if err != nil {
			return err
		}
		if _, err := s.initialize(); err != nil {
			s.finish()
			return err
		}
		tr, err := s.turn("Remember the word "+st.word+". Reply with just: ok", turnTimeout, nil)
		if err != nil {
			s.finish()
			return err
		}
		sessionID := str(tr.result.Body["session_id"])
		s.observe("stop", st.stop(s))
		s.finish()

		r, err := start(startOpts{fixture: "e8-resume-" + st.name, args: commonArgs("--resume", sessionID), dir: dir})
		if err != nil {
			return err
		}
		if _, err := r.initialize(); err != nil {
			r.finish()
			return err
		}
		tr, err = r.turn("What word did I ask you to remember? Reply with the word only.", turnTimeout, nil)
		if err != nil {
			r.finish()
			return err
		}
		r.observe("resumed_session_id", tr.result.Body["session_id"])
		r.observe("same_session_id", str(tr.result.Body["session_id"]) == sessionID)
		r.observe("context_restored", contains(tr.text, st.word))
		r.finish()
	}
	return nil
}

// E9: compare --safe-mode with a normal start. The project has a CLAUDE.md
// instruction and a project settings allow rule, so the turn shows whether
// each is honoured.
func e9SafeMode() error {
	for _, safe := range []bool{false, true} {
		dir, err := workdir("e9")
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("End every reply with the word BANANA.\n"), 0o644); err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Join(dir, ".claude"), 0o755); err != nil {
			return err
		}
		settings := `{"permissions":{"allow":["Bash(touch:*)"]}}`
		if err := os.WriteFile(filepath.Join(dir, ".claude", "settings.json"), []byte(settings), 0o644); err != nil {
			return err
		}

		args := []string{"--model", *model}
		name := "e9-normal"
		if safe {
			args = append(args, "--safe-mode")
			name = "e9-safe-mode"
		}
		s, err := start(startOpts{fixture: name, args: args, dir: dir})
		if err != nil {
			return err
		}
		if _, err := s.initialize(); err != nil {
			s.finish()
			return err
		}
		var init msg
		if err := s.user("Run exactly `touch marker.txt` with the Bash tool and reply with just: done"); err != nil {
			s.finish()
			return err
		}
		permissions := 0
		var text string
		res, err := s.until(turnTimeout, func(m msg) {
			if m.Subtype == "init" {
				init = m
			}
			text += textDelta(m)
			if isPermissionRequest(m) {
				permissions++
				s.answerPermission(m, allowAll)
			}
		}, func(m msg) bool { return m.Type == "result" })
		if err != nil {
			s.finish()
			return err
		}
		s.observe("result_subtype", res.Subtype)
		s.observe("auth_ok", res.Body["is_error"] == false)
		s.observe("claude_md_honoured", contains(text, "banana"))
		s.observe("project_allow_rule_honoured", permissions == 0)
		s.observe("init_mcp_servers", init.Body["mcp_servers"])
		s.observe("init_plugins", init.Body["plugins"])
		s.observe("init_skills", init.Body["skills"])
		s.observe("init_agents", init.Body["agents"])
		s.observe("init_tool_count", length(init.Body["tools"]))
		s.observe("init_slash_command_count", length(init.Body["slash_commands"]))
		s.observe("init_memory_paths", init.Body["memory_paths"])
		s.finish()
	}
	return nil
}

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }

func str(v any) string { s, _ := v.(string); return s }

func length(v any) int { a, _ := v.([]any); return len(a) }

func keys(v any) []string {
	m, _ := v.(map[string]any)
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

// exitCode is -1 while the process is still running.
func exitCode(s *session) int {
	select {
	case <-s.exited:
		return s.cmd.ProcessState.ExitCode()
	default:
		return -1
	}
}
