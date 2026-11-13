package approval

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/roomscript/coderoom/internal/agent"
)

func fixture(t *testing.T) Policy {
	t.Helper()
	root := t.TempDir()
	isolateGitTestEnvironment(t)
	t.Setenv("HOME", root)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, ".config"))
	t.Setenv("PATH", "/usr/bin:/bin")
	if err := os.Mkdir(filepath.Join(root, "docs"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "design.md"), []byte("approval"), 0600); err != nil {
		t.Fatal(err)
	}
	return Policy{ProjectRoot: root,
		Ignored: func(context.Context, string) (bool, error) { return false, nil }}
}

func TestFileContainment(t *testing.T) {
	p := fixture(t)
	if err := os.Symlink(t.TempDir(), filepath.Join(p.ProjectRoot, "outside")); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		path           string
		write, allowed bool
	}{
		{"docs/design.md", false, true}, {"docs/new.md", true, true}, {"outside/new", true, false},
		{"docs/design.md", true, true},
		{"../outside", true, false}, {"AGENTS.md", true, false}, {"docs/AGENTS.md", true, false},
		{".git/config", true, false},
	} {
		t.Run(tt.path, func(t *testing.T) {
			got := p.Evaluate(context.Background(), agent.FileOperation{Cwd: p.ProjectRoot, Paths: []string{tt.path}, Write: tt.write, AllTargetsKnown: true})
			if (got.Outcome == Approve) != tt.allowed {
				t.Fatalf("result = %+v", got)
			}
		})
	}
}

func TestHardLinkedWriteTargetsRequireReview(t *testing.T) {
	for _, name := range []string{"AGENTS.md", "ignored.yml", "outside-project", "ordinary.md"} {
		t.Run(name, func(t *testing.T) {
			p := fixture(t)
			source := filepath.Join(p.ProjectRoot, name)
			if name == "outside-project" {
				source = filepath.Join(t.TempDir(), "outside.md")
			}
			if err := os.WriteFile(source, []byte("fixture"), 0600); err != nil {
				t.Fatal(err)
			}
			p.Ignored = func(_ context.Context, path string) (bool, error) {
				return name == "ignored.yml" && path == source, nil
			}
			alias := filepath.Join(p.ProjectRoot, "docs", "ordinary.md")
			if err := os.Link(source, alias); err != nil {
				t.Fatal(err)
			}
			e := agent.FileOperation{Cwd: p.ProjectRoot, Paths: []string{alias}, Write: true, AllTargetsKnown: true}
			result := p.Evaluate(context.Background(), e)
			if result.Outcome != Review || result.Reason != "file.target_unsupported" {
				t.Fatalf("hard-linked write result = %+v", result)
			}
			e.Write = false
			if result := p.Evaluate(context.Background(), e); result.Outcome != Approve {
				t.Fatalf("hard-linked read result = %+v", result)
			}
		})
	}
}

func TestProtectedWriteNamesIgnoreCase(t *testing.T) {
	for _, tt := range []struct {
		path      string
		protected bool
	}{
		{"agents.md", true}, {"AgEnTs.Md", true}, {"docs/agents.MD", true},
		{".GIT/config", true}, {".GiT/HEAD", true},
		{"docs/agents.md.bak", false}, {".github/config", false},
	} {
		t.Run(tt.path, func(t *testing.T) {
			p := fixture(t)
			target := filepath.Join(p.ProjectRoot, tt.path)
			if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(target, []byte("fixture"), 0600); err != nil {
				t.Fatal(err)
			}
			alias := filepath.Join(p.ProjectRoot, "docs", "alias.md")
			if err := os.Symlink(target, alias); err != nil {
				t.Fatal(err)
			}
			for _, path := range []string{target, alias} {
				assertWriteProtection(t, p, path, tt.protected)
			}
		})
	}
}

func assertWriteProtection(t *testing.T, p Policy, path string, wantProtected bool) {
	t.Helper()
	e := agent.FileOperation{Cwd: p.ProjectRoot, Paths: []string{path}, Write: true, AllTargetsKnown: true}
	result := p.Evaluate(context.Background(), e)
	if wantProtected {
		if result.Outcome != Review || result.Reason != "file.protected" {
			t.Fatalf("protected write result = %+v", result)
		}
	} else if result.Outcome != Approve {
		t.Fatalf("ordinary write result = %+v", result)
	}
	e.Write = false
	if result := p.Evaluate(context.Background(), e); result.Outcome != Approve {
		t.Fatalf("read result = %+v", result)
	}
}

func TestIgnoredPathDecisions(t *testing.T) {
	for _, tt := range []struct {
		name                 string
		ignored, unavailable bool
		reason               string
	}{
		{"ignored config", true, false, "file.ignored_or_unknown"},
		{"ignore check failure", false, true, "file.ignored_or_unknown"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p := fixture(t)
			p.Ignored = func(context.Context, string) (bool, error) {
				if tt.unavailable {
					return false, errors.New("unavailable")
				}
				return tt.ignored, nil
			}
			result := p.Evaluate(context.Background(), agent.FileOperation{Paths: []string{"docs/design.md"}, Cwd: p.ProjectRoot, Write: true, AllTargetsKnown: true})
			if result.Outcome != Review || result.Reason != tt.reason {
				t.Fatalf("result = %+v", result)
			}
		})
	}
}

func TestContainmentIsReevaluatedAfterSymlinkChange(t *testing.T) {
	p := fixture(t)
	link := filepath.Join(p.ProjectRoot, "link")
	if err := os.Symlink(filepath.Join(p.ProjectRoot, "docs"), link); err != nil {
		t.Fatal(err)
	}
	e := agent.FileOperation{Paths: []string{"link/new.md"}, Cwd: p.ProjectRoot, Write: true, AllTargetsKnown: true}
	if result := p.Evaluate(context.Background(), e); result.Outcome != Approve {
		t.Fatalf("result = %+v", result)
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), link); err != nil {
		t.Fatal(err)
	}
	if result := p.Evaluate(context.Background(), e); result.Outcome != Review {
		t.Fatal("reused a stale containment grant")
	}
}

// Git approval fixtures must not inherit the caller's pager or Git overrides.
func isolateGitTestEnvironment(t *testing.T) {
	t.Helper()
	t.Setenv("PAGER", "cat")
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if !strings.HasPrefix(key, "GIT_") {
			continue
		}
		// Setenv registers restoration; an empty value still counts as an override
		// to the policy, so remove the variable entirely for the fixture.
		t.Setenv(key, "")
		if err := os.Unsetenv(key); err != nil {
			t.Fatal(err)
		}
	}
}
