package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/roomscript/coderoom/internal/transcript"
)

func TestRunBindsPromptUUID(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "output.transcript")
	var fixture bytes.Buffer
	steps := []transcript.Step{
		{Kind: "recv", Match: map[string]any{"type": "user", "uuid": "recorded"}},
		{Kind: "send", Message: map[string]any{"type": "user", "uuid": "recorded", "isReplay": true}},
	}
	if err := transcript.WriteOutput(&fixture, transcript.Output{Name: "test", Backend: "claude"}, steps); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, fixture.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	input, err := os.CreateTemp(dir, "stdin")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = input.Close() }()
	if _, err := input.WriteString("{\"type\":\"user\",\"uuid\":\"fresh\"}\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := input.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	output, err := os.CreateTemp(dir, "stdout")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = output.Close() }()
	oldArgs, oldIn, oldOut := os.Args, os.Stdin, os.Stdout
	os.Args, os.Stdin, os.Stdout = []string{"claude-replay", "--transcript", path}, input, output
	defer func() { os.Args, os.Stdin, os.Stdout = oldArgs, oldIn, oldOut }()
	if err := run(); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(output.Name())
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "{\"isReplay\":true,\"type\":\"user\",\"uuid\":\"fresh\"}\n" {
		t.Fatalf("output=%s", got)
	}
}

func TestResolveTranscriptPath(t *testing.T) {
	for _, tt := range []struct {
		name    string
		args    []string
		wantErr bool
	}{
		{"valid", []string{"--transcript", "fixture.transcript"}, false},
		{"missing", nil, true},
		{"missing path", []string{"--transcript"}, true},
		{"wrong flag", []string{"--file", "fixture.transcript"}, true},
		{"extra argument", []string{"--transcript", "fixture.transcript", "extra"}, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			path, err := resolveTranscriptPath(tt.args)
			if (err != nil) != tt.wantErr {
				t.Fatalf("error=%v, wantErr=%t", err, tt.wantErr)
			}
			if !tt.wantErr && path != "fixture.transcript" {
				t.Fatalf("path=%q", path)
			}
		})
	}
}
