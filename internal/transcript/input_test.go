package transcript

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadInputDir_Prompt(t *testing.T) {
	dir := t.TempDir()
	writeInputFile(t, filepath.Join(dir, "prompt.md"), `---
model: test-model
options:
  permission: ask
  workspace: restricted
---
Create example.txt`)

	got, err := ReadInputDir(dir)
	if err != nil {
		t.Fatalf("ReadInputDir: %v", err)
	}
	if got.Config.Model != "test-model" || got.Config.Options["permission"] != "ask" || got.Config.Options["workspace"] != "restricted" {
		t.Fatalf("configuration = %+v", got.Config)
	}
	if len(got.Actions) != 1 || got.Actions[0].Kind != "prompt" {
		t.Fatalf("actions = %#v, want single prompt", got.Actions)
	}
	if !strings.Contains(got.Actions[0].Text, "example.txt") {
		t.Fatalf("prompt = %q, want file name", got.Actions[0].Text)
	}
}

func TestReadInputDir_PromptReasoningSettings(t *testing.T) {
	root := t.TempDir()
	writeInputFile(t, filepath.Join(root, "prompt.md"), `---
model: gpt-5.4
system_prompt: "Treat TOKEN_7Q9 as the value 2."
effort: extensive
options:
  summary: verbose
---
prompt`)

	got, err := ReadInputDir(root)
	if err != nil {
		t.Fatalf("ReadInputDir: %v", err)
	}
	if got.Config.Effort != "extensive" || got.Config.Options["summary"] != "verbose" {
		t.Fatalf("configuration = %+v", got.Config)
	}
	if got.Config.SystemPrompt != "Treat TOKEN_7Q9 as the value 2." {
		t.Fatalf("system_prompt = %q", got.Config.SystemPrompt)
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

func TestInputConfigurationSchema(t *testing.T) {
	tests := []struct {
		name, header string
		wantErr      bool
	}{
		{"empty", "---", false},
		{"neutral options", "---\noptions:\n  backend_specific: arbitrary", false},
		{"legacy system prompt", "---\ndeveloper_instructions: old", true},
		{"legacy effort", "---\nreasoning_effort: low", true},
		{"legacy option", "---\nsandbox: read-only", true},
		{"unknown field", "---\nunknown: value", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseInputFrontMatter([]byte(tt.header))
			if (err != nil) != tt.wantErr {
				t.Fatalf("error=%v, want error=%t", err, tt.wantErr)
			}
		})
	}
}
