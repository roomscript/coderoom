package claude

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// writeFakeClaude puts a claude script on a PATH of its own. The script
// exits with versionExit when asked for its version.
func writeFakeClaude(t *testing.T, versionExit string) {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\n[ \"$1\" = --version ] && exit " + versionExit + "\necho \"unexpected: $*\" >&2\nexit 1\n"
	if err := os.WriteFile(filepath.Join(dir, "claude"), []byte(script), 0o755); err != nil { //nolint:gosec // test script must be executable
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
}

// firstFailure runs each check in order and returns the index of the first
// failure, or -1 when all pass.
func firstFailure(t *testing.T, c *Client) int {
	t.Helper()
	for i, dep := range c.ExternalDependencies() {
		if err := dep.Verify(context.Background()); err != nil {
			t.Logf("%s: %v", dep.Description, err)
			return i
		}
	}
	return -1
}

func TestExternalDependencies(t *testing.T) {
	tests := []struct {
		name        string
		versionExit string // exit code of the fake claude --version; empty means no claude
		opts        []Option
		wantChecks  int
		wantFailure int
	}{
		{"installed claude passes", "0", nil, 2, -1},
		{"missing claude fails the first check", "", nil, 2, 0},
		{"failing --version fails the second check", "1", nil, 2, 1},
		{"command override present", "0", []Option{WithCommand("claude")}, 1, -1},
		{"command override missing", "0", []Option{WithCommand("no-such-peer")}, 1, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.versionExit != "" {
				writeFakeClaude(t, tt.versionExit)
			} else {
				t.Setenv("PATH", t.TempDir())
			}
			c := New(t.TempDir(), tt.opts...)

			if got := len(c.ExternalDependencies()); got != tt.wantChecks {
				t.Fatalf("checks = %d, want %d", got, tt.wantChecks)
			}
			if got := firstFailure(t, c); got != tt.wantFailure {
				t.Fatalf("first failing check = %d, want %d", got, tt.wantFailure)
			}
		})
	}
}
