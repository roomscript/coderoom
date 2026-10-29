package codex_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/roomscript/coderoom/internal/transcript"
)

func TestTranscriptFixturesRoundTrip(t *testing.T) {
	paths, err := filepath.Glob("testdata/transcripts/*/*/output.transcript")
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatal("no transcript fixtures found")
	}
	for _, path := range paths {
		t.Run(transcriptCaseName(path), func(t *testing.T) {
			original, err := os.ReadFile(filepath.Clean(path))
			if err != nil {
				t.Fatal(err)
			}
			output, steps, err := transcript.ReadOutput(bytes.NewReader(original))
			if err != nil {
				t.Fatal(err)
			}
			var rewritten bytes.Buffer
			if err := transcript.WriteOutput(&rewritten, output, steps); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(original, rewritten.Bytes()) {
				t.Fatal("read/write changed fixture bytes")
			}
		})
	}
}
