package config

import (
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestLoadReportsEveryBrokenFile(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		".coderoom/participants/ada.yaml": "alias: [ada\n",
		".coderoom/participants/tim.yaml": "alias: tim\nrole: reviewer\n",
		".coderoom/participants/bob.yaml": "alias: bob\n",
	})

	s, err := Load(root, testDefaults)
	if err == nil {
		t.Fatal("expected error")
	}
	if s == nil || !slices.Equal(slices.Sorted(maps.Keys(s.participants)), []string{"bob"}) {
		t.Fatalf("partial config = %+v, want only bob", s)
	}
	var joined interface{ Unwrap() []error }
	if !errors.As(err, &joined) {
		t.Fatalf("expected joined errors, got %v", err)
	}
	if got := len(joined.Unwrap()); got != 2 {
		t.Fatalf("errors = %d, want 2: %v", got, err)
	}
	if !errors.Is(err, errInvalidParticipantYAML) || !errors.Is(err, errRolePromptNotFound) {
		t.Fatalf("expected both file errors, got %v", err)
	}
}

func TestLoadRejectsEmptyDefaultBackend(t *testing.T) {
	t.Parallel()
	if _, err := Load(t.TempDir(), Defaults{Backend: BackendRequest{Name: " ", Source: "default"}}); err == nil {
		t.Fatal("expected error")
	}
}

func TestLoadIgnoresOtherEntries(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		".coderoom/participants/README.md":         "not a participant",
		".coderoom/participants/ada.yml":           "alias: [broken\n",
		".coderoom/participants/nested/tim.yaml":   "alias: [broken\n",
		".coderoom/participants/ada.yaml":          "alias: ada\n",
		".coderoom/prompts/roles/unreferenced.md":  "unused",
		".coderoom/prompts/roles/also-ignored.txt": "unused",
	})

	s, err := Load(root, testDefaults)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := slices.Sorted(maps.Keys(s.participants)); !slices.Equal(got, []string{"ada"}) {
		t.Fatalf("participants = %v, want [ada]", got)
	}
}

func TestConfiguredBackends(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		".coderoom/participants/ada.yaml": "alias: ada\nbackend: claude\n",
		".coderoom/participants/tim.yaml": "alias: tim\n",
	})

	s, err := Load(root, Defaults{Backend: BackendRequest{Name: "cluade", Source: "CODEROOM_BACKEND"}})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := map[string]string{
		".coderoom/participants/ada.yaml": "claude",
		"CODEROOM_BACKEND":                "cluade",
	}
	if got := s.ConfiguredBackends(); !maps.Equal(got, want) {
		t.Fatalf("ConfiguredBackends() = %v, want %v", got, want)
	}
}

func TestChangedSources(t *testing.T) {
	t.Parallel()
	const (
		adaFile  = ".coderoom/participants/ada.yaml"
		roleFile = ".coderoom/prompts/roles/reviewer.md"
		timFile  = ".coderoom/participants/tim.yaml"
	)
	tests := []struct {
		name  string
		alias string
		edit  func(t *testing.T, root string)
		want  []string
	}{
		{"unchanged", "ada", func(*testing.T, string) {}, nil},
		{"same content rewritten", "ada", writeFile(adaFile, "alias: ada\nrole: reviewer\n"), nil},
		{"participant file edited", "ada", writeFile(adaFile, "alias: ada\nrole: reviewer\nbackend: claude\n"), []string{adaFile}},
		{"role prompt edited", "ada", writeFile(roleFile, "Focus on tests."), []string{roleFile}},
		{"participant file deleted", "ada", removeFile(adaFile), []string{adaFile}},
		{"broken edit is still only a change", "ada", writeFile(adaFile, "alias: [ada\n"), []string{adaFile}},
		{"file created for an alias without one", "tim", writeFile(timFile, "alias: tim\n"), []string{timFile}},
		{"alias without a file stays unchanged", "tim", func(*testing.T, string) {}, nil},
		{"other alias's edit is not reported", "tim", writeFile(roleFile, "Focus on tests."), nil},
		{"invalid alias", "../ada", func(*testing.T, string) {}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			writeFiles(t, root, map[string]string{
				adaFile:  "alias: ada\nrole: reviewer\n",
				roleFile: "Focus on correctness.",
			})
			s, err := Load(root, testDefaults)
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			tt.edit(t, root)

			got := s.ChangedSources(tt.alias)
			want := make([]string, 0, len(tt.want))
			for _, p := range tt.want {
				want = append(want, filepath.FromSlash(p))
			}
			if !slices.Equal(got, want) {
				t.Fatalf("ChangedSources(%q) = %v, want %v", tt.alias, got, want)
			}
		})
	}
}

func writeFile(name, content string) func(*testing.T, string) {
	return func(t *testing.T, root string) {
		t.Helper()
		writeFiles(t, root, map[string]string{name: content})
	}
}

func removeFile(name string) func(*testing.T, string) {
	return func(t *testing.T, root string) {
		t.Helper()
		if err := os.Remove(filepath.Join(root, filepath.FromSlash(name))); err != nil {
			t.Fatal(err)
		}
	}
}
