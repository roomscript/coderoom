package transcript

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/roomscript/coderoom/internal/agent/codex"
)

func TestReadInputDir_Prompt(t *testing.T) {
	dir := filepath.Join("..", "agent", "codex", "testdata", "transcripts", "0.133.0", "approvals-file-change")

	got, err := ReadInputDir(dir)
	if err != nil {
		t.Fatalf("ReadInputDir: %v", err)
	}
	if got.Config.Model != "gpt-5.4" {
		t.Fatalf("model = %q, want gpt-5.4", got.Config.Model)
	}
	if got.Config.AskForApproval != codex.AskOnRequest {
		t.Fatalf("ask_for_approval = %q, want %q", got.Config.AskForApproval, codex.AskOnRequest)
	}
	if got.Config.Sandbox != codex.SandboxReadOnly {
		t.Fatalf("sandbox = %q, want %q", got.Config.Sandbox, codex.SandboxReadOnly)
	}
	if got.Config.ReasoningEffort != codex.ReasoningDefault {
		t.Fatalf("reasoning_effort = %q, want empty default", got.Config.ReasoningEffort)
	}
	if got.Config.ReasoningSummary != codex.ReasoningSummaryDefault {
		t.Fatalf("reasoning_summary = %q, want empty default", got.Config.ReasoningSummary)
	}
	if len(got.Actions) != 1 || got.Actions[0].Kind != "prompt" {
		t.Fatalf("actions = %#v, want single prompt", got.Actions)
	}
	if !strings.Contains(got.Actions[0].Text, "codex_file_approval_test.txt") {
		t.Fatalf("prompt = %q, want file name", got.Actions[0].Text)
	}
}

func TestReadInputDir_PromptReasoningSettings(t *testing.T) {
	root := t.TempDir()
	writeInputFile(t, filepath.Join(root, "prompt.md"), `---
model: gpt-5.4
developer_instructions: "Treat TOKEN_7Q9 as the value 2."
reasoning_effort: xhigh
reasoning_summary: detailed
---
prompt`)

	got, err := ReadInputDir(root)
	if err != nil {
		t.Fatalf("ReadInputDir: %v", err)
	}
	if got.Config.ReasoningEffort != codex.ReasoningXHigh {
		t.Fatalf("reasoning_effort = %q, want %q", got.Config.ReasoningEffort, codex.ReasoningXHigh)
	}
	if got.Config.ReasoningSummary != codex.ReasoningSummaryDetailed {
		t.Fatalf("reasoning_summary = %q, want %q", got.Config.ReasoningSummary, codex.ReasoningSummaryDetailed)
	}
	if got.Config.DeveloperInstructions != "Treat TOKEN_7Q9 as the value 2." {
		t.Fatalf("developer_instructions = %q", got.Config.DeveloperInstructions)
	}
}

func TestReadInputDir_Conversation(t *testing.T) {
	root := t.TempDir()
	writeInputFile(t, filepath.Join(root, "conversation.md"), `---
model: gpt-5.4
---
`)
	writeInputFile(t, filepath.Join(root, "conversation-01.md"), `---
kind: notice
---
The magic word is PICASSO.`)
	writeInputFile(t, filepath.Join(root, "conversation-02.md"), `Reply with a single word: done`)

	got, err := ReadInputDir(root)
	if err != nil {
		t.Fatalf("ReadInputDir: %v", err)
	}
	if got.Config.Model != "gpt-5.4" {
		t.Fatalf("model = %q, want gpt-5.4", got.Config.Model)
	}
	if len(got.Actions) != 2 {
		t.Fatalf("actions = %d, want 2", len(got.Actions))
	}
	if got.Actions[0].Kind != "notice" || got.Actions[0].Text != "The magic word is PICASSO." {
		t.Fatalf("first action = %#v", got.Actions[0])
	}
	if got.Actions[1].Kind != "prompt" || got.Actions[1].Text != "Reply with a single word: done" {
		t.Fatalf("second action = %#v", got.Actions[1])
	}
}

func TestReadInputDir_RejectsUnknownActionKey(t *testing.T) {
	root := t.TempDir()
	writeInputFile(t, filepath.Join(root, "conversation.md"), "---\nmodel: gpt-5.4\n---\n")
	writeInputFile(t, filepath.Join(root, "conversation-01.md"), `---
unexpected: true
---
hello`)

	if _, err := ReadInputDir(root); err == nil {
		t.Fatal("ReadInputDir succeeded, want error")
	}
}

func TestReadInputDir_RejectsMissingSequence(t *testing.T) {
	root := t.TempDir()
	writeInputFile(t, filepath.Join(root, "conversation.md"), "---\nmodel: gpt-5.4\n---\n")
	writeInputFile(t, filepath.Join(root, "conversation-02.md"), "hello")

	if _, err := ReadInputDir(root); err == nil {
		t.Fatal("ReadInputDir succeeded, want error")
	}
}

func writeInputFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile(%q): %v", path, err)
	}
}
