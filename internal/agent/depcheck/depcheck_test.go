package depcheck

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeTool puts an executable script called tool on a PATH of its own.
func writeTool(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "tool"), []byte("#!/bin/sh\n"+body), 0o755); err != nil { //nolint:gosec // test script must be executable
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	return dir
}

func TestInstalled(t *testing.T) {
	writeTool(t, "exit 0\n")
	if err := Installed("tool").Verify(context.Background()); err != nil {
		t.Errorf("present tool: %v", err)
	}
	if err := Installed("missing").Verify(context.Background()); err == nil {
		t.Error("expected error for a missing tool")
	}
}

func TestReportsVersion(t *testing.T) {
	// The tool succeeds only when asked for the version of pkg.
	writeTool(t, "[ \"$1\" = pkg ] && [ \"$2\" = --version ] && exit 0\necho \"unexpected: $*\" >&2\nexit 1\n")
	tests := []struct {
		name    string
		args    []string
		wantErr bool
	}{
		{"version runs", []string{"pkg"}, false},
		{"version fails", []string{"other"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dep := ReportsVersion(t.TempDir(), "tool", tt.args...)
			if dep.Description != "tool "+tt.args[0]+" --version runs" {
				t.Errorf("description = %q", dep.Description)
			}
			if err := dep.Verify(context.Background()); (err != nil) != tt.wantErr {
				t.Errorf("Verify error = %v, want error %v", err, tt.wantErr)
			}
		})
	}
}

func TestReportsVersionStopsOnCancel(t *testing.T) {
	writeTool(t, "exit 0\n")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := ReportsVersion(t.TempDir(), "tool").Verify(ctx); err == nil {
		t.Fatal("expected error from a cancelled check")
	}
}

func TestReportsVersionCancelKillsChildren(t *testing.T) {
	marks := t.TempDir()
	started := filepath.Join(marks, "started")
	beat := filepath.Join(marks, "beat")
	systemPath := os.Getenv("PATH")
	// The tool stands in for a launcher whose child outlives it unless killed.
	dir := writeTool(t, "(while :; do echo x >> '"+beat+"'; sleep 0.02; done) &\n"+
		"touch '"+started+"'\n"+
		"wait\n")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+systemPath)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	dep := ReportsVersion(t.TempDir(), "tool")
	go func() { done <- dep.Verify(ctx) }()
	waitForFile(t, started)
	waitForFile(t, beat)
	cancel()
	if err := <-done; err == nil {
		t.Fatal("expected error from a cancelled check")
	}

	before := fileSize(t, beat)
	time.Sleep(200 * time.Millisecond)
	if after := fileSize(t, beat); after != before {
		t.Fatalf("child still running after cancellation: heartbeat grew from %d to %d bytes", before, after)
	}
}

func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", path)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func fileSize(t *testing.T, path string) int64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Size()
}
