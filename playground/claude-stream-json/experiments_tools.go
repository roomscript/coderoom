package main

import (
	"fmt"
	"path/filepath"
	"time"
)

// E10: tools that ask the human, act outside a turn, or reach outside the
// room. Each variant runs in its own process. SendMessage, PushNotification
// and RemoteTrigger are not triggered: they have real effects outside the
// playground.
func e10Tools() error {
	for _, v := range []struct {
		name string
		run  func(string) error
	}{
		{"ask-user-question", e10AskUserQuestion},
		{"plan-mode", e10PlanMode},
		{"background-bash", e10BackgroundBash},
		{"cron", e10Cron},
		{"list-agents", e10ListAgents},
	} {
		if err := v.run("e10-" + v.name); err != nil {
			return fmt.Errorf("%s: %w", v.name, err)
		}
	}
	return nil
}

// recordingPolicy wraps policy and records the tool name of every request.
func recordingPolicy(names *[]string, policy permissionPolicy) permissionPolicy {
	return func(tool string, input map[string]any) decision {
		*names = append(*names, tool)
		return policy(tool, input)
	}
}

func startE10(fixture string, extra ...string) (*session, string, error) {
	dir, err := workdir(fixture)
	if err != nil {
		return nil, "", err
	}
	s, err := start(startOpts{fixture: fixture, args: commonArgs(extra...), dir: dir})
	if err != nil {
		return nil, "", err
	}
	if _, err := s.initialize(); err != nil {
		s.finish()
		return nil, "", err
	}
	return s, dir, nil
}

// e10AskUserQuestion answers one question, then dismisses a second one.
func e10AskUserQuestion(fixture string) error {
	s, _, err := startE10(fixture)
	if err != nil {
		return err
	}
	defer s.finish()

	answer := func(tool string, input map[string]any) decision {
		if tool != "AskUserQuestion" {
			return denyAll(tool, input)
		}
		s.observe("ask_input", input)
		return decision{allow: true, input: answerLastOption(input)}
	}
	tr, err := s.turn("Use the AskUserQuestion tool to ask me whether I prefer red or blue (single choice). After I answer, reply with only my choice.", turnTimeout, answer)
	if err != nil {
		return err
	}
	s.observe("answered", map[string]any{"tools": tr.tools, "permissions": tr.permissions, "reply": tr.text})

	dismiss := func(string, map[string]any) decision {
		return decision{message: "The user dismissed the question without answering."}
	}
	tr, err = s.turn("Use the AskUserQuestion tool again to ask me whether I prefer cats or dogs. After I answer, reply with only my choice.", turnTimeout, dismiss)
	if err != nil {
		return err
	}
	s.observe("dismissed", map[string]any{"tools": tr.tools, "permissions": tr.permissions, "reply": tr.text})
	return nil
}

// answerLastOption picks the last option of every question, in the
// documented updatedInput format: questions passed through, answers keyed
// by question text.
func answerLastOption(input map[string]any) map[string]any {
	questions, _ := input["questions"].([]any)
	answers := map[string]any{}
	for _, q := range questions {
		qm, _ := q.(map[string]any)
		opts, _ := qm["options"].([]any)
		if len(opts) == 0 {
			continue
		}
		last, _ := opts[len(opts)-1].(map[string]any)
		answers[str(qm["question"])] = last["label"]
	}
	return map[string]any{"questions": questions, "answers": answers}
}

// e10PlanMode starts in plan mode and approves whatever is asked.
func e10PlanMode(fixture string) error {
	s, dir, err := startE10(fixture, "--permission-mode", "plan")
	if err != nil {
		return err
	}
	defer s.finish()

	var asked []string
	tr, err := s.turn("Create a file hello.txt containing hi in the current directory. Do not ask questions.", turnTimeout, recordingPolicy(&asked, allowAll))
	if err != nil {
		return err
	}
	s.observe("plan_turn", map[string]any{"tools": tr.tools, "permission_tools": asked, "reply": tr.text})
	s.observe("file_created", fileExists(filepath.Join(dir, "hello.txt")))
	return nil
}

// e10BackgroundBash starts a background command, ends the turn, and
// watches for output that arrives without a new user message.
func e10BackgroundBash(fixture string) error {
	s, _, err := startE10(fixture)
	if err != nil {
		return err
	}
	defer s.finish()

	tr, err := s.turn("Run `sleep 8 && echo finished` with the Bash tool using run_in_background set to true. Do not wait for it. Reply with just: started", turnTimeout, allowAll)
	if err != nil {
		return err
	}
	s.observe("start_turn_tools", tr.tools)
	s.watchIdle(40*time.Second, allowAll)

	tr, err = s.turn("What did the background command print? Reply with just its output.", turnTimeout, allowAll)
	if err != nil {
		return err
	}
	s.observe("followup", map[string]any{"tools": tr.tools, "reply": tr.text})
	return nil
}

// e10Cron schedules a one-off prompt and watches for it to fire.
func e10Cron(fixture string) error {
	s, _, err := startE10(fixture)
	if err != nil {
		return err
	}
	defer s.finish()

	var asked []string
	tr, err := s.turn("Use the CronCreate tool to schedule the prompt 'Reply with just: tick' to run once, about one minute from now. Then reply with just: scheduled", turnTimeout, recordingPolicy(&asked, allowAll))
	if err != nil {
		return err
	}
	s.observe("schedule_turn", map[string]any{"tools": tr.tools, "permission_tools": asked, "reply": tr.text})
	s.watchIdle(100*time.Second, allowAll)
	return nil
}

// e10ListAgents checks whether a participant can see other local sessions.
func e10ListAgents(fixture string) error {
	s, _, err := startE10(fixture)
	if err != nil {
		return err
	}
	defer s.finish()

	var asked []string
	noSend := func(tool string, input map[string]any) decision {
		if tool == "SendMessage" {
			return decision{message: "Sending messages is not allowed in this test."}
		}
		return decision{allow: true}
	}
	tr, err := s.turn("Use the ListAgents tool and reply with only the number of entries it lists. Do not send any messages.", turnTimeout, recordingPolicy(&asked, noSend))
	if err != nil {
		return err
	}
	s.observe("list_turn", map[string]any{"tools": tr.tools, "permission_tools": asked, "reply": tr.text})
	return nil
}

// watchIdle reads for d without sending anything and records every message
// that arrives: output between turns that coderoom did not ask for.
func (s *session) watchIdle(d time.Duration, policy permissionPolicy) {
	logf("  watching idle for %s", d)
	t0 := time.Now()
	var seen []string
	var permissions int
	deadline := time.After(d)
	for {
		select {
		case m, ok := <-s.lines:
			if !ok {
				s.observe("idle_process_exited", true)
				s.observe("idle_messages", seen)
				return
			}
			seen = append(seen, fmt.Sprintf("%dms %s", time.Since(t0).Milliseconds(), eventKey(m)))
			if d := textDelta(m); d != "" {
				logf("  idle text: %q", d)
			}
			s.handleControl(m, policy, &permissions)
		case <-deadline:
			s.observe("idle_messages", seen)
			return
		}
	}
}
