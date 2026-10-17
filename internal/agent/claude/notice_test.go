package claude

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/roomscript/coderoom/internal/agent"
)

func TestNoticeFilter(t *testing.T) {
	tests := []struct {
		name       string
		deltas     []string
		wantRelay  string // reasoning relayed for Read(); empty when suppressed
		wantRelays bool
	}{
		{"acknowledgement", []string{`{"acknow`, `ledge":true}`}, "", false},
		{"acknowledgement with extra fields", []string{`{"acknowledge":true,"note":"ok"}`}, "", false},
		{"leading blank text", []string{"\n", ` {"acknowledge":true}`}, "", false},
		{"json that is not an acknowledgement", []string{`{"acknowledge":false}`}, `{"acknowledge":false}`, true},
		{"plain reply", []string{"\n", "Got it, ", "thanks."}, "\nGot it, thanks.", true},
		{"no reply", nil, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var f noticeFilter
			f.reset()
			var msgs []agent.Message
			for _, d := range tt.deltas {
				msgs = append(msgs, f.filter([]agent.Message{{StreamID: "claude:output:m:0", Mode: agent.ModeStream, Content: agent.Output{Text: d}}})...)
			}
			msgs = append(msgs, f.filter([]agent.Message{{StreamID: "claude:output:m:0", Mode: agent.ModeFlush, Content: agent.Output{}}})...)
			msgs = append(msgs, f.finish()...)

			if !tt.wantRelays {
				if len(msgs) != 0 {
					t.Errorf("messages = %+v, want none", msgs)
				}
				return
			}
			got := accumulate(t, msgs)
			if r, _ := got.Content.(agent.Reasoning); got.StreamID != noticeRelayStreamID || r.Text != tt.wantRelay {
				t.Errorf("relayed %+v, want reasoning %q on %s", got, tt.wantRelay, noticeRelayStreamID)
			}
		})
	}
}

func TestNoticeFilter_passesToolsAndDropsThinking(t *testing.T) {
	var f noticeFilter
	f.reset()
	cmd := agent.Message{StreamID: "claude:command:t1", Mode: agent.ModeStream, Content: agent.Command{Command: "ls"}}
	thinking := agent.Message{StreamID: "claude:reasoning:m:0", Mode: agent.ModeStream, Content: agent.Reasoning{Text: "hmm"}}

	got := f.filter([]agent.Message{thinking, cmd})
	if len(got) != 1 || got[0].StreamID != cmd.StreamID {
		t.Errorf("filtered = %+v, want only the command", got)
	}
}

func TestSendNotice_acknowledged(t *testing.T) {
	obs := &recordingObserver{}
	notice := noticeContextPrefix + "ada joined"
	c := startPeer(t, peerScript(
		[]string{lineInitOK},
		[]string{lineSystemInit, replayLine(notice), lineMessageStart, lineTextStart,
			textDeltaLine(`{\"acknowledge\":true}`), lineBlockStop, lineResultOK},
	), WithObserver(obs))

	anchor, err := c.SendNotice("ada joined")
	if err != nil {
		t.Fatal(err)
	}
	if anchor != noticeTurnStreamID {
		t.Errorf("anchor = %q, want %q", anchor, noticeTurnStreamID)
	}
	msgs := readUntil(t, c, func(m agent.Message) bool { return m.StreamID == noticeTurnStreamID && m.Mode == agent.ModeFlush })
	if len(msgs) != 1 {
		t.Errorf("messages = %+v, want only the notice anchor flush", msgs)
	}
	if sent := obs.sentLines(); !strings.Contains(sent[len(sent)-1], "[CONTEXT UPDATE") {
		t.Errorf("notice sent without the prefix: %s", sent[len(sent)-1])
	}
	if _, err := c.Send("next"); err != nil {
		t.Errorf("Send after the notice: %v", err)
	}
}

func TestSendNotice_replyRelayedAndToolsKept(t *testing.T) {
	notice := noticeContextPrefix + "ada joined"
	c := startPeer(t, peerScript(
		[]string{lineInitOK},
		[]string{
			lineSystemInit, replayLine(notice), lineMessageStart, lineTextStart, textDeltaLine("Hello ada"), lineBlockStop,
			toolUseLine("toolu_1", "Bash", `{"command":"ls"}`),
			toolResultLine("toolu_1", `"a.txt"`, false, `{"stdout":"a.txt","stderr":""}`),
			lineResultOK,
		},
	))

	if _, err := c.SendNotice("ada joined"); err != nil {
		t.Fatal(err)
	}
	msgs := readUntil(t, c, func(m agent.Message) bool { return m.StreamID == noticeTurnStreamID && m.Mode == agent.ModeFlush })
	if slices.ContainsFunc(msgs, isReplyOutput) {
		t.Errorf("notice reply delivered as output: %+v", msgs)
	}
	byStream := map[agent.StreamID][]agent.Message{}
	for _, m := range msgs[:len(msgs)-1] {
		byStream[m.StreamID] = append(byStream[m.StreamID], m)
	}
	if r, _ := accumulate(t, byStream[noticeRelayStreamID]).Content.(agent.Reasoning); r.Text != "Hello ada" {
		t.Errorf("relayed reasoning = %q", r.Text)
	}
	if cmd, _ := accumulate(t, byStream["claude:command:toolu_1"]).Content.(agent.Command); cmd.Output != "a.txt" {
		t.Errorf("command during notice = %+v, want it delivered", cmd)
	}
}

// isReplyOutput reports output other than the notice anchor flush.
func isReplyOutput(m agent.Message) bool {
	_, ok := m.Content.(agent.Output)
	return ok && m.StreamID != noticeTurnStreamID
}

func TestSendNotice_turnInProgress(t *testing.T) {
	c := startPeer(t, peerScript(
		[]string{lineInitOK},
		[]string{lineSystemInit, replayLine("first")},
		// The turn ends only after the test interrupts it.
		[]string{lineResultOK},
	))

	if _, err := c.Send("first"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.SendNotice("ada joined"); !errors.Is(err, agent.ErrTurnInProgress) {
		t.Errorf("SendNotice during a turn: error = %v, want ErrTurnInProgress", err)
	}
	if err := c.Interrupt(); err != nil {
		t.Fatal(err)
	}
	readUntil(t, c, isAnchorFlush)
}

func TestSendNotice_failedTurn(t *testing.T) {
	failed := `{"type":"result","subtype":"error_during_execution","is_error":true,"terminal_reason":"api_error"}`
	c := startPeer(t, peerScript([]string{lineInitOK}, []string{lineSystemInit, failed}))

	if _, err := c.SendNotice("ada joined"); err != nil {
		t.Fatal(err)
	}
	msgs := readUntil(t, c, func(m agent.Message) bool { return m.StreamID == noticeTurnStreamID && m.Mode == agent.ModeFlush })
	if len(msgs) != 2 || !isLog(msgs[0]) {
		t.Errorf("messages = %+v, want a log then the notice anchor flush", msgs)
	}
}
