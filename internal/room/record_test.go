package room

import (
	"testing"

	"github.com/roomscript/coderoom/internal/agent"
)

func TestNewAgentRecordSetsKindAndText(t *testing.T) {
	tests := []struct {
		name string
		msg  agent.Message
		kind Kind
		text string
	}{
		{
			name: "output",
			msg:  agent.Message{StreamID: "out1", Mode: agent.ModeStream, Content: agent.Output{Text: "hello"}},
			kind: KindAgentOutput,
			text: "hello",
		},
		{
			name: "reasoning",
			msg:  agent.Message{StreamID: "r1", Mode: agent.ModeStream, Content: agent.Reasoning{Text: "think"}},
			kind: KindReasoning,
			text: "think",
		},
		{
			name: "command",
			msg:  agent.Message{StreamID: "c1", Mode: agent.ModeStream, Content: agent.Command{Output: "stdout"}},
			kind: KindCommand,
			text: "stdout",
		},
		{
			name: "file change",
			msg: agent.Message{StreamID: "f1", Mode: agent.ModeStream, Content: agent.FileChangeSet{
				Changes: []agent.FileChange{{Path: "a.txt", Diff: "+hi\n", ChangeKind: "update"}},
			}},
			kind: KindFileChange,
			text: "=== a.txt (update)\n+hi\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			record := NewAgentRecord("ada", tt.msg)
			if record.Kind != tt.kind {
				t.Fatalf("expected kind %v, got %v", tt.kind, record.Kind)
			}
			if record.Text != tt.text {
				t.Fatalf("expected text %q, got %q", tt.text, record.Text)
			}
			if record.Msg == nil {
				t.Fatal("expected message payload")
			}
		})
	}
}

func TestRecordAccumulateUpdatesTextAndMessage(t *testing.T) {
	record := NewAgentRecord("ada", agent.Message{
		StreamID: "cmd1",
		Mode:     agent.ModeStream,
		Content:  agent.Command{Command: "echo hi", Output: "h"},
	})
	next, err := record.Accumulate(agent.Message{
		StreamID: "cmd1",
		Mode:     agent.ModeStream,
		Content:  agent.Command{Output: "i"},
	})
	if err != nil {
		t.Fatalf("Accumulate() error = %v", err)
	}
	if next.Text != "hi" {
		t.Fatalf("expected accumulated text %q, got %q", "hi", next.Text)
	}
	if next.Msg == nil {
		t.Fatal("expected accumulated message payload")
	}
	cmd, ok := next.Msg.Content.(agent.Command)
	if !ok {
		t.Fatalf("expected Command content, got %T", next.Msg.Content)
	}
	if cmd.Output != "hi" {
		t.Fatalf("expected accumulated output %q, got %q", "hi", cmd.Output)
	}
}

func TestFormatFileChangeBody(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		if got := formatFileChangeBody(nil); got != "" {
			t.Fatalf("expected empty string, got %q", got)
		}
	})

	t.Run("appends missing trailing newline", func(t *testing.T) {
		got := formatFileChangeBody([]agent.FileChange{
			{Path: "a.txt", ChangeKind: "update", Diff: "diff --git a/a.txt b/a.txt\n+hi"},
		})
		want := "=== a.txt (update)\ndiff --git a/a.txt b/a.txt\n+hi\n"
		if got != want {
			t.Fatalf("unexpected body:\nwant:\n%q\ngot:\n%q", want, got)
		}
	})

	t.Run("multiple separated by blank line", func(t *testing.T) {
		got := formatFileChangeBody([]agent.FileChange{
			{Path: "a.txt", Diff: "A\n"},
			{Path: "b.txt", ChangeKind: "add", Diff: "B"},
		})
		want := "=== a.txt\nA\n\n=== b.txt (add)\nB\n"
		if got != want {
			t.Fatalf("unexpected body:\nwant:\n%q\ngot:\n%q", want, got)
		}
	})
}

func TestRecordAccumulateWithoutMessageFails(t *testing.T) {
	_, err := (Record{}).Accumulate(agent.Message{
		StreamID: "out1",
		Mode:     agent.ModeStream,
		Content:  agent.Output{Text: "hello"},
	})
	if err == nil {
		t.Fatal("expected error for record without message")
	}
}
