package approval

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/roomscript/coderoom/internal/agent"
)

func TestShellDecisions(t *testing.T) {
	p := fixture(t)
	if err := os.Symlink(t.TempDir(), filepath.Join(p.ProjectRoot, "outside")); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		command string
		outcome Outcome
	}{
		{"ls -la docs", Approve},
		{"cat docs/design.md | head -n 20", Approve},
		{"git status --short; git diff --stat; echo done", Approve},
		{"cd docs && ls && head -n 20 design.md", Approve},
		{"/bin/sh -c 'git status --short && git diff --stat'", Approve},
		{"/bin/bash --noprofile --norc -c '/bin/sh -c \"ls docs\"'", Approve},
		{"echo '$(rm -rf .)'", Approve},
		{"echo 'a; b > c'", Approve},
		{"grep -n -e approval docs/design.md || echo absent", Approve},
		{"git show HEAD:docs/design.md", Approve},
		{"ls; rm -rf docs", Review},
		{"git diff && git push", Review},
		{"echo ok | sh", Review},
		{"git diff --output=/tmp/change.patch", Review},
		{"echo \"$(cat ~/.ssh/id_ed25519)\"", Review},
		{"cat /etc/passwd", Review},
		{"cat outside/file", Review},
		{"/bin/sh -lc 'ls'", Review},
		{"/bin/sh --unknown -c 'ls'", Review},
		{"/bin/zsh -lc 'ls'", Review},
		{"echo hi > docs/new", Review},
		{"ls &", Review},
		{"cat docs/*", Review},
		{"PATH=. ls", Review},
		{"for x in docs; do ls; done", Review},
		{"ls || rm docs/design.md", Review},
		{"cd docs || ls", Review},
		{"grep -r approval docs", Review},
	} {
		t.Run(tt.command, func(t *testing.T) {
			result := p.Evaluate(context.Background(), agent.CommandOperation{Command: tt.command, Cwd: p.ProjectRoot})
			if result.Outcome != tt.outcome {
				t.Fatalf("got %+v; want %s", result, tt.outcome)
			}
		})
	}
}

func TestShellExecutableAndGitFacts(t *testing.T) {
	p := fixture(t)
	t.Setenv("PATH", p.ProjectRoot)
	if err := os.WriteFile(filepath.Join(p.ProjectRoot, "cat"), []byte("#!/bin/sh\nexit 99\n"), 0700); err != nil { //nolint:gosec // executable fixture must never run
		t.Fatal(err)
	}
	op := agent.CommandOperation{Command: "cat docs/design.md", Cwd: p.ProjectRoot}
	if got := p.Evaluate(context.Background(), op); got.Reason != "shell.executable_unvalidated" {
		t.Fatalf("got %+v", got)
	}
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "core.fsmonitor")
	t.Setenv("GIT_CONFIG_VALUE_0", "PRIVATE")
	op.Command = "/usr/bin/git status"
	if got := p.Evaluate(context.Background(), op); got.Reason != "shell.git_config_unvalidated" {
		t.Fatalf("got %+v", got)
	}
}

func TestWrapperArguments(t *testing.T) {
	for _, tt := range []struct {
		name    string
		args    []string
		allowed bool
	}{
		{"sh", []string{"-c", "ls"}, true},
		{"bash", []string{"--noprofile", "--norc", "-c", "ls"}, true},
		{"zsh", []string{"-f", "-c", "ls"}, true},
		{"zsh", []string{"-c", "ls"}, false},
		{"zsh", []string{"-lc", "ls"}, false},
		{"sh", []string{"-c", "ls", "extra"}, false},
		{"sh", []string{"--unknown", "-c", "ls"}, false},
	} {
		t.Run(tt.name+strings.Join(tt.args, " "), func(t *testing.T) {
			_, ok := wrapperBody(tt.name, tt.args)
			if ok != tt.allowed {
				t.Fatalf("allowed=%v", ok)
			}
		})
	}
}

func TestShellStartupEnvironment(t *testing.T) {
	p := fixture(t)
	for _, name := range []string{"BASH_ENV", "ENV", "CDPATH", "SHELLOPTS", "BASH_FUNC_cat%%"} {
		t.Run(name, func(t *testing.T) {
			t.Setenv(name, "PRIVATE")
			got := p.Evaluate(context.Background(), agent.CommandOperation{Command: "cat docs/design.md", Cwd: p.ProjectRoot})
			if got.Reason != "shell.startup_unvalidated" {
				t.Fatalf("got %+v", got)
			}
		})
	}
}

func TestGitApprovalFixtureIgnoresHostOverrides(t *testing.T) {
	t.Setenv("PAGER", "less")
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "core.fsmonitor")
	t.Setenv("GIT_CONFIG_VALUE_0", "untrusted-helper")
	p := fixture(t)
	op := agent.CommandOperation{Command: "git status --short", Cwd: p.ProjectRoot}
	if got := p.Evaluate(context.Background(), op); got.Outcome != Approve {
		t.Fatalf("got %+v", got)
	}
	// The fixture reset does not relax production validation of later overrides.
	t.Setenv("PAGER", "less")
	if got := p.Evaluate(context.Background(), op); got.Reason != "shell.git_config_unvalidated" {
		t.Fatalf("got %+v", got)
	}
}
