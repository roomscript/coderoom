package codex_test

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"testing"

	"github.com/roomscript/coderoom/internal/transcript"
)

type liveTranscriptCase struct {
	name  string
	input transcript.Input
}

func discoverSmokeTranscriptCases(t *testing.T, versionDir string) []liveTranscriptCase {
	t.Helper()

	entries, err := os.ReadDir(versionDir)
	if err != nil {
		t.Fatalf("ReadDir(%q): %v", versionDir, err)
	}
	var names []string
	for _, entry := range entries {
		if entry.IsDir() {
			names = append(names, entry.Name())
		}
	}
	slices.Sort(names)

	cases := make([]liveTranscriptCase, 0, len(names))
	for _, name := range names {
		caseDir := filepath.Join(versionDir, name)
		input, err := transcript.ReadInputDir(caseDir)
		if err != nil {
			t.Fatalf("ReadInputDir(%q): %v", caseDir, err)
		}
		if slices.ContainsFunc(input.Actions, func(action transcript.Action) bool { return transcript.IsModelAction(action.Kind) }) {
			continue
		}
		cases = append(cases, liveTranscriptCase{
			name:  name,
			input: input,
		})
	}
	return cases
}

// This discovery check is offline: no client is created and no agent is contacted.
func TestDiscoverSmokeTranscriptCasesExcludesModelActions(t *testing.T) {
	dir := t.TempDir()
	kinds := []string{"prompt", "notice", "model-list", "model-select", "model-select-failed", "model-interrupt", "model-settings"}
	for _, kind := range kinds {
		// Do not use the model-* directory naming convention.
		caseDir := filepath.Join(dir, "case-"+strconv.Itoa(len(kind))+"-"+kind)
		if err := os.MkdirAll(caseDir, 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(caseDir, "conversation.md"), []byte("---\n---\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		action := "---\nkind: " + kind + "\n---\nexample\n"
		if err := os.WriteFile(filepath.Join(caseDir, "conversation-01.md"), []byte(action), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cases := discoverSmokeTranscriptCases(t, dir)
	var got []string
	for _, tc := range cases {
		got = append(got, tc.input.Actions[0].Kind)
	}
	slices.Sort(got)
	if !slices.Equal(got, []string{"notice", "prompt"}) {
		t.Fatalf("smoke action kinds=%v", got)
	}
}
