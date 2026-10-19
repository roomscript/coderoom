package codex

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// writeFakeNpx puts an npx script on a PATH of its own. The script succeeds
// only when asked for the version of wantPkg.
func writeFakeNpx(t *testing.T, wantPkg string) {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\n" +
		"[ \"$1\" = \"" + wantPkg + "\" ] && [ \"$2\" = \"--version\" ] && exit 0\n" +
		"echo \"unexpected: $*\" >&2\nexit 1\n"
	if err := os.WriteFile(filepath.Join(dir, "npx"), []byte(script), 0o755); err != nil { //nolint:gosec // test script must be executable
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
}

// verifyAll runs each check in order and returns the index of the first
// failure, or -1 when all pass.
func verifyAll(t *testing.T, c *Client) int {
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
		npxPkg      string // package the fake npx accepts; empty means no npx
		override    string // CODEX_VERSION_OVERRIDE
		opts        []Option
		wantChecks  int
		wantFailure int
	}{
		{"default package passes", "@openai/codex", "", nil, 2, -1},
		{"version override is checked", "@openai/codex@1.2.3", "1.2.3", nil, 2, -1},
		{"version override mismatch fails the run check", "@openai/codex", "1.2.3", nil, 2, 1},
		{"missing npx fails the first check", "", "", nil, 2, 0},
		{"app-server override present", "@openai/codex", "", []Option{WithAppServerCommand("npx")}, 1, -1},
		{"app-server override missing", "@openai/codex", "", []Option{WithAppServerCommand("no-such-codex")}, 1, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.npxPkg != "" {
				writeFakeNpx(t, tt.npxPkg)
			} else {
				t.Setenv("PATH", t.TempDir())
			}
			t.Setenv("CODEX_VERSION_OVERRIDE", tt.override)
			c := New(t.TempDir(), tt.opts...)

			if got := len(c.ExternalDependencies()); got != tt.wantChecks {
				t.Fatalf("checks = %d, want %d", got, tt.wantChecks)
			}
			if got := verifyAll(t, c); got != tt.wantFailure {
				t.Fatalf("first failing check = %d, want %d", got, tt.wantFailure)
			}
		})
	}
}
