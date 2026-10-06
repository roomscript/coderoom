package shell_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/roomscript/coderoom/internal/shell"
)

func TestRun_success(t *testing.T) {
	result := shell.Run(
		context.Background(),
		t.TempDir(),
		`printf 'standard output'; printf 'standard error' >&2`,
	)

	if result.Status != shell.StatusSuccess {
		t.Fatalf("status = %q, want success", result.Status)
	}
	assertExitCode(t, result.ExitCode, 0)
	if result.Stdout != "standard output" {
		t.Errorf("stdout = %q, want %q", result.Stdout, "standard output")
	}
	if result.Stderr != "standard error" {
		t.Errorf("stderr = %q, want %q", result.Stderr, "standard error")
	}
	if result.Err != nil {
		t.Errorf("unexpected execution error: %v", result.Err)
	}
}

func TestRun_nonZeroExit(t *testing.T) {
	result := shell.Run(
		context.Background(),
		t.TempDir(),
		`printf 'partial output'; printf 'failure details' >&2; exit 7`,
	)

	if result.Status != shell.StatusFailure {
		t.Fatalf("status = %q, want failure", result.Status)
	}
	assertExitCode(t, result.ExitCode, 7)
	if result.Stdout != "partial output" {
		t.Errorf("stdout = %q, want %q", result.Stdout, "partial output")
	}
	if result.Stderr != "failure details" {
		t.Errorf("stderr = %q, want %q", result.Stderr, "failure details")
	}
	if result.Err != nil {
		t.Errorf("non-zero exit should not be an execution error: %v", result.Err)
	}
}

func TestRun_usesWorkingDirectoryAndEnvironment(t *testing.T) {
	cwd := t.TempDir()
	t.Setenv("CODEROOM_SHELL_TEST", "inherited")
	result := shell.Run(
		context.Background(),
		cwd,
		`printf '%s\n%s' "$PWD" "$CODEROOM_SHELL_TEST"`,
	)

	if result.Status != shell.StatusSuccess {
		t.Fatalf("status = %q, want success", result.Status)
	}
	want := cwd + "\ninherited"
	if result.Stdout != want {
		t.Errorf("stdout = %q, want %q", result.Stdout, want)
	}
}

func TestRun_startFailure(t *testing.T) {
	missingCwd := filepath.Join(t.TempDir(), "missing")
	result := shell.Run(context.Background(), missingCwd, "exit 0")

	if result.Status != shell.StatusFailure {
		t.Fatalf("status = %q, want failure", result.Status)
	}
	if result.ExitCode != nil {
		t.Errorf("exit code = %d, want nil", *result.ExitCode)
	}
	if result.Err == nil {
		t.Fatal("expected process-start error")
	}
}

func TestRun_signalTerminationHasNoExitCode(t *testing.T) {
	result := shell.Run(context.Background(), t.TempDir(), `kill -TERM $$`)

	if result.Status != shell.StatusFailure {
		t.Fatalf("status = %q, want failure", result.Status)
	}
	if result.ExitCode != nil {
		t.Errorf("exit code = %d, want nil", *result.ExitCode)
	}
	if result.Err != nil {
		t.Errorf("signal termination should not be an execution error: %v", result.Err)
	}
}

func TestRun_cancellation(t *testing.T) {
	cwd := t.TempDir()
	started := filepath.Join(cwd, "started")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	resultCh := make(chan shell.Result, 1)
	go func() {
		resultCh <- shell.Run(ctx, cwd, `touch started; sleep 300 | cat`)
	}()

	waitForFile(t, started)
	cancel()

	select {
	case result := <-resultCh:
		if result.Status != shell.StatusCancelled {
			t.Fatalf("status = %q, want cancelled", result.Status)
		}
		if result.ExitCode != nil {
			t.Errorf("exit code = %d, want nil", *result.ExitCode)
		}
		if !errors.Is(result.Err, context.Canceled) {
			t.Errorf("error = %v, want context cancellation", result.Err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("shell process did not stop after cancellation")
	}
}

func TestRun_cancelsBackgroundChildAfterShellExit(t *testing.T) {
	cwd := t.TempDir()
	started := filepath.Join(cwd, "started")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	resultCh := make(chan shell.Result, 1)
	go func() {
		resultCh <- shell.Run(ctx, cwd, `sleep 300 & touch started`)
	}()

	waitForFile(t, started)
	time.Sleep(25 * time.Millisecond)
	cancel()

	select {
	case result := <-resultCh:
		if result.Status != shell.StatusCancelled {
			t.Fatalf("status = %q, want cancelled", result.Status)
		}
		if !errors.Is(result.Err, context.Canceled) {
			t.Errorf("error = %v, want context cancellation", result.Err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("background child blocked shell cancellation")
	}
}

func TestRun_backgroundChildDoesNotBlockSuccessfulShell(t *testing.T) {
	cwd := t.TempDir()
	resultCh := make(chan shell.Result, 1)
	go func() {
		resultCh <- shell.Run(context.Background(), cwd, `sleep 300 &`)
	}()

	select {
	case result := <-resultCh:
		if result.Status != shell.StatusSuccess {
			t.Fatalf("status = %q, error = %v, want success", result.Status, result.Err)
		}
		assertExitCode(t, result.ExitCode, 0)
	case <-time.After(2 * time.Second):
		t.Fatal("background child blocked successful shell completion")
	}
}

func assertExitCode(t *testing.T, got *int, want int) {
	t.Helper()
	if got == nil {
		t.Fatalf("exit code = nil, want %d", want)
	}
	if *got != want {
		t.Errorf("exit code = %d, want %d", *got, want)
	}
}

func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("shell process did not start")
}
