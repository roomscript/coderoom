package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/roomscript/coderoom/internal/transcript"
	"go.uber.org/goleak"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

func TestRecordingOptions(t *testing.T) {
	tests := []struct {
		name    string
		config  transcript.Config
		wantErr bool
	}{
		{"defaults", transcript.Config{}, false},
		{"model and prompt", transcript.Config{Model: "haiku", SystemPrompt: "brief"}, false},
		{"accept", transcript.Config{Options: map[string]string{"approval": "accept"}}, false},
		{"decline", transcript.Config{Options: map[string]string{"approval": "decline"}}, false},
		{"effort", transcript.Config{Effort: "high"}, true},
		{"unknown option", transcript.Config{Options: map[string]string{"sandbox": "read-only"}}, true},
		{"unknown approval", transcript.Config{Options: map[string]string{"approval": "cancel"}}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			options, err := recordingOptions(tt.config)
			if (err != nil) != tt.wantErr {
				t.Fatalf("error=%v, wantErr=%t", err, tt.wantErr)
			}
			if !tt.wantErr && len(options) != 2 {
				t.Fatalf("options=%d, want 2", len(options))
			}
		})
	}
}

func TestValidateActions(t *testing.T) {
	for _, kind := range []string{"", "prompt", "notice", "model-list", "model-select", "model-select-failed", "model-settings", "model-interrupt", "unknown"} {
		t.Run(kind, func(t *testing.T) {
			err := validateActions([]transcript.Action{{Kind: kind}})
			wantErr := kind == "model-interrupt" || kind == "unknown"
			if (err != nil) != wantErr {
				t.Fatalf("error=%v, wantErr=%t", err, wantErr)
			}
		})
	}
}

func TestRedactRecording(t *testing.T) {
	raw := json.RawMessage(`{"email":"private@example.test","organization":"private-org","path":"/home/test/file","nested":[{"path":"/home/test/echo"}],"count":2}`)
	output := transcript.Output{Expect: transcript.Expect{Output: transcript.TextExpectation{Content: "echo /home/test/file"}, FileChange: transcript.FileChangeExpectation{Files: []string{"/home/test/file"}}}}
	steps := []transcript.Step{{Kind: "recv", Match: map[string]json.RawMessage{"payload": raw}}, {Kind: "send", Message: map[string]json.RawMessage{"payload": raw}}}
	got, redacted, err := redactRecording(output, steps, "/home/test")
	if err != nil {
		t.Fatal(err)
	}
	assertRedactedOutput(t, got, redacted)
	if got.Expect.Output.Content != "echo ~/file" || !reflect.DeepEqual(got.Expect.FileChange.Files, []string{"~/file"}) {
		t.Fatalf("expectations=%+v", got.Expect)
	}
	recv := redacted[0].Match.(map[string]any)["payload"]
	send := redacted[1].Message.(map[string]any)["payload"]
	if !reflect.DeepEqual(recv, send) {
		t.Fatal("echoed values differ between directions")
	}
	payload := recv.(map[string]any)
	if payload["email"] != "<redacted>" || payload["organization"] != "<redacted>" || payload["path"] != "~/file" {
		t.Fatalf("payload=%v", payload)
	}
	if !strings.Contains(string(steps[0].Match.(map[string]json.RawMessage)["payload"]), "private@example.test") || output.Expect.Output.Content != "echo /home/test/file" {
		t.Fatal("redaction mutated source")
	}
}

func assertRedactedOutput(t *testing.T, output transcript.Output, steps []transcript.Step) {
	t.Helper()
	var encoded bytes.Buffer
	if err := transcript.WriteOutput(&encoded, output, steps); err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"private@example.test", "private-org", "/home/test"} {
		if strings.Contains(encoded.String(), secret) {
			t.Fatalf("recording retained %q", secret)
		}
	}
}

func TestCheckInstalledVersion(t *testing.T) {
	setFakeClaudeVersion(t, "2.1.304")
	if err := checkInstalledVersion("2.1.304"); err != nil {
		t.Fatal(err)
	}
	if err := checkInstalledVersion("2.1.303"); err == nil {
		t.Fatal("expected version mismatch")
	}
}

func TestAuthoringScenarios(t *testing.T) {
	cases, err := resolveAllCases(filepath.Join("..", "..", transcriptRoot))
	if err != nil {
		t.Fatal(err)
	}
	if len(cases) == 0 {
		t.Fatal("no authoring scenarios found")
	}
	for _, tc := range cases {
		t.Run(tc.version+"/"+tc.name, func(t *testing.T) {
			input, err := transcript.ReadInputDir(tc.dir)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := recordingOptions(input.Config); err != nil {
				t.Fatal(err)
			}
			if err := validateActions(input.Actions); err != nil {
				t.Fatal(err)
			}
			if tc.name != "turn-failed" && input.Config.Model != "haiku" {
				t.Fatalf("model=%q", input.Config.Model)
			}
		})
	}
}

func TestResolveSelection(t *testing.T) {
	setFakeClaudeVersion(t, "2.1.305")
	root := t.TempDir()
	for _, path := range []string{
		filepath.Join("2.1.304", "alpha"),
		filepath.Join("2.1.304", "beta"),
		filepath.Join("2.1.305", "alpha"),
	} {
		if err := os.MkdirAll(filepath.Join(root, path), 0o750); err != nil {
			t.Fatal(err)
		}
	}
	for _, tt := range []struct {
		name    string
		args    []string
		count   int
		wantErr bool
	}{
		{"installed version", nil, 1, false},
		{"baseline version", []string{"2.1.304"}, 2, false},
		{"new version", []string{"2.1.305"}, 1, false},
		{"case", []string{"2.1.304", "alpha"}, 1, false},
		{"missing version", []string{"missing"}, 0, true},
		{"extra arguments", []string{"a", "b", "c"}, 0, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cases, err := resolveSelection(root, tt.args)
			if (err != nil) != tt.wantErr {
				t.Fatalf("error=%v, wantErr=%t", err, tt.wantErr)
			}
			if !tt.wantErr && len(cases) != tt.count {
				t.Fatalf("cases=%d, want %d", len(cases), tt.count)
			}
		})
	}
}

func setFakeClaudeVersion(t *testing.T, version string) {
	t.Helper()
	dir := t.TempDir()
	// #nosec G306 -- This test fixture is an executable fake CLI.
	if err := os.WriteFile(filepath.Join(dir, "claude"), []byte("#!/bin/sh\necho '"+version+" (Claude Code)'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
}

func TestResolveSelectionUnavailableInstalledVersion(t *testing.T) {
	for _, installed := range []bool{true, false} {
		t.Run(fmt.Sprint(installed), func(t *testing.T) {
			if installed {
				setFakeClaudeVersion(t, "2.1.306")
			} else {
				t.Setenv("PATH", t.TempDir())
			}
			if _, err := resolveSelection(t.TempDir(), nil); err == nil {
				t.Fatal("expected error without installed version scenarios")
			}
		})
	}
}

func TestRedactHomePathBoundaries(t *testing.T) {
	for _, tt := range []struct{ name, input, home, want string }{
		{"whole home", "/home/test", "/home/test", "~"},
		{"child path", "/home/test/file", "/home/test", "~/file"},
		{"other home", "/home/testx/file", "/home/test", "/home/testx/file"},
		{"similar name", "/home/testing", "/home/test", "/home/testing"},
		{"embedded paths", "read /home/test/a and /home/test/b", "/home/test", "read ~/a and ~/b"},
		{"mixed paths", "/home/testx/a /home/test/b", "/home/test", "/home/testx/a ~/b"},
		{"empty home", "/home/test/file", "", "/home/test/file"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := redactValue(tt.input, tt.home); got != tt.want {
				t.Fatalf("redacted=%q, want %q", got, tt.want)
			}
		})
	}
}

func resolveAllCases(root string) ([]transcriptCase, error) {
	versions, err := listDirNames(root)
	if err != nil {
		return nil, err
	}
	var cases []transcriptCase
	for _, version := range versions {
		versionCases, err := resolveVersionCases(root, version)
		if err != nil {
			return nil, err
		}
		cases = append(cases, versionCases...)
	}
	return cases, nil
}
