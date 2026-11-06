package transcript

import (
	"reflect"
	"strings"
	"testing"

	"github.com/roomscript/coderoom/internal/agent"
)

type scenarioClient struct {
	modelActionClient
	sent    []string
	notices []string
}

func (*scenarioClient) Name() string { return "fake" }
func (*scenarioClient) Start() error { return nil }
func (*scenarioClient) Stop() error  { return nil }
func (c *scenarioClient) Send(text string) (agent.StreamID, error) {
	c.sent = append(c.sent, text)
	c.messages = append(c.messages,
		agent.Message{StreamID: "output", Mode: agent.ModeStream, Content: agent.Output{Text: "OK"}},
		agent.Message{StreamID: "output", Mode: agent.ModeFlush, Content: agent.Output{}},
		agent.Message{StreamID: "prompt", Mode: agent.ModeFlush, Content: agent.Output{}},
	)
	return "prompt", nil
}
func (c *scenarioClient) SendNotice(text string) (agent.StreamID, error) {
	c.notices = append(c.notices, text)
	c.messages = append(c.messages, agent.Message{StreamID: "notice", Mode: agent.ModeFlush, Content: agent.Output{}})
	return "notice", nil
}

func TestRunActions(t *testing.T) {
	actions := []Action{{Kind: "model-list", Text: "test-model"}, {Kind: "prompt", Text: "hello"}, {Kind: "notice", Text: "context"}}
	client := &scenarioClient{}
	collector := NewCollector(CollectorOptions{NoticeAnchor: "notice"})
	if err := RunActions(client, actions, collector, NewTurnActivityObserver(nil, nil)); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(client.sent, []string{"hello"}) || !reflect.DeepEqual(client.notices, []string{"context"}) {
		t.Fatalf("sent=%v notices=%v", client.sent, client.notices)
	}
	if len(client.messages) != 0 {
		t.Fatal("runner left unread messages")
	}
	expected := Expect{Output: TextExpectation{NumMessages: 2, Content: "OK"}, Reasoning: ReasoningExpectation{AllFlushed: true}, Notice: &NoticeExpectation{NumTurnFlushes: 1}}
	if got := collector.Expectations(actions, nil); !reflect.DeepEqual(got, expected) {
		t.Fatalf("expectations=%+v, want %+v", got, expected)
	}
	if err := CheckExpectations(expected, collector, NewReplayApprovalListener(nil)); err != nil {
		t.Fatal(err)
	}
}

func TestCollectorBackendRules(t *testing.T) {
	c := NewCollector(CollectorOptions{
		NoticeAnchor:  "notice",
		IgnoreLog:     func(id agent.StreamID) bool { return id == "stderr" },
		NormalizePath: func(path string) string { return strings.TrimPrefix(path, "/recording/") },
	})
	for _, msg := range []agent.Message{
		{StreamID: "stderr", Mode: agent.ModeSingle, Content: agent.Log{Text: "noise"}},
		{StreamID: "log", Mode: agent.ModeSingle, Content: agent.Log{Text: "diagnostic"}},
		{StreamID: "files", Mode: agent.ModeStream, Content: agent.FileChangeSet{Changes: []agent.FileChange{{Path: "/recording/file.txt"}}}},
		{StreamID: "files", Mode: agent.ModeFlush, Content: agent.FileChangeSet{}},
	} {
		c.Observe(msg)
	}
	expected := Expect{Log: TextExpectation{NumMessages: 1, Content: "diagnostic"}, Reasoning: ReasoningExpectation{AllFlushed: true}, FileChange: FileChangeExpectation{NumMessages: 2, Files: []string{"file.txt"}}}
	if got := c.Expectations(nil, nil); !reflect.DeepEqual(got, expected) {
		t.Fatalf("expectations=%+v, want %+v", got, expected)
	}
	if err := CheckExpectations(expected, c, NewReplayApprovalListener(nil)); err != nil {
		t.Fatal(err)
	}
}

func TestCollectorRejectsInvalidStreams(t *testing.T) {
	tests := []struct {
		name     string
		messages []agent.Message
	}{
		{"missing command exit", []agent.Message{
			{StreamID: "command", Mode: agent.ModeStream, Content: agent.Command{Command: "true"}},
			{StreamID: "command", Mode: agent.ModeFlush, Content: agent.Command{}},
		}},
		{"file flush payload", []agent.Message{
			{StreamID: "files", Mode: agent.ModeStream, Content: agent.FileChangeSet{}},
			{StreamID: "files", Mode: agent.ModeFlush, Content: agent.FileChangeSet{Status: agent.ToolStatusCompleted}},
		}},
		{"empty reasoning", []agent.Message{{StreamID: "reasoning", Mode: agent.ModeStream, Content: agent.Reasoning{}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := NewCollector(CollectorOptions{})
			for _, msg := range tt.messages {
				c.Observe(msg)
			}
			if err := CheckExpectations(c.Expectations(nil, nil), c, NewReplayApprovalListener(nil)); err == nil {
				t.Fatal("expected lifecycle error")
			}
		})
	}
}
