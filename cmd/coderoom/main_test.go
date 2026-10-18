package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/roomscript/coderoom/internal/agent/registry"
	"github.com/roomscript/coderoom/internal/config"
	"go.uber.org/goleak"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

func TestConfigDefaults(t *testing.T) {
	builtin := config.BackendRequest{Name: "codex", Source: "built-in default"}
	tests := []struct {
		name  string
		value *string // nil means unset
		want  config.BackendRequest
	}{
		{"unset", nil, builtin},
		{"blank", ptr(""), builtin},
		{"whitespace only", ptr("  \t"), builtin},
		{"set", ptr("claude"), config.BackendRequest{Name: "claude", Source: "CODEROOM_BACKEND"}},
		{"whitespace padded", ptr("  claude \n"), config.BackendRequest{Name: "claude", Source: "CODEROOM_BACKEND"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(backendEnvVar, "") // restores the variable after the test
			if tt.value == nil {
				if err := os.Unsetenv(backendEnvVar); err != nil {
					t.Fatal(err)
				}
			} else {
				t.Setenv(backendEnvVar, *tt.value)
			}

			if got := configDefaults().Backend; got != tt.want {
				t.Fatalf("configDefaults().Backend = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestLoadConfigReportsFileAndBackendErrorsTogether(t *testing.T) {
	root := t.TempDir()
	writeParticipant(t, root, "ada", "alias: [ada\n")              // malformed
	writeParticipant(t, root, "tim", "alias: tim\nbackend: abc\n") // unknown backend
	backends, err := registry.NewDefault(root)
	if err != nil {
		t.Fatal(err)
	}
	defaults := config.Defaults{Backend: config.BackendRequest{Name: "cluade", Source: "CODEROOM_BACKEND"}}

	_, err = loadConfig(root, defaults, backends)
	if got := countLeafErrors(err); got != 3 {
		t.Fatalf("errors = %d, want 3 (ada.yaml, tim.yaml, CODEROOM_BACKEND): %v", got, err)
	}
}

func writeParticipant(t *testing.T, root, alias, content string) {
	t.Helper()
	dir := filepath.Join(root, ".coderoom", "participants")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, alias+".yaml"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// countLeafErrors counts the errors inside any nesting of errors.Join.
func countLeafErrors(err error) int {
	var joined interface{ Unwrap() []error }
	if !errors.As(err, &joined) {
		if err == nil {
			return 0
		}
		return 1
	}
	n := 0
	for _, e := range joined.Unwrap() {
		n += countLeafErrors(e)
	}
	return n
}

func ptr(s string) *string { return &s }
