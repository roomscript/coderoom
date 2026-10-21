package codex_test

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/roomscript/coderoom/internal/transcript"
)

func TestModelTranscriptInputsMatchRecordings(t *testing.T) {
	paths, err := filepath.Glob("testdata/transcripts/*/model-*/output.transcript")
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatal("no model transcript cases found")
	}
	for _, path := range paths {
		t.Run(transcriptCaseName(path), func(t *testing.T) {
			input, err := transcript.ReadInputDir(filepath.Dir(path))
			if err != nil {
				t.Fatal(err)
			}
			output, err := readTranscriptFixture(path)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(input.Actions, transcript.NormalizedActions(output)) {
				t.Fatalf("conversation actions differ from recorded actions: input=%+v output=%+v", input.Actions, output.Actions)
			}
		})
	}
}
