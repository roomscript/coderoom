package main

import (
	"testing"

	"github.com/roomscript/coderoom/internal/transcript"
)

func TestRecordingOptions(t *testing.T) {
	tests := []struct {
		name    string
		config  transcript.Config
		wantErr bool
	}{
		{"defaults", transcript.Config{}, false},
		{"settings", transcript.Config{Model: "model", SystemPrompt: "system", Effort: "low", Options: map[string]string{"ask_for_approval": "on-request", "sandbox": "read-only", "reasoning_summary": "detailed"}}, false},
		{"unknown option", transcript.Config{Options: map[string]string{"unknown": "value"}}, true},
		{"invalid approval", transcript.Config{Options: map[string]string{"ask_for_approval": "invalid"}}, true},
		{"invalid sandbox", transcript.Config{Options: map[string]string{"sandbox": "invalid"}}, true},
		{"invalid summary", transcript.Config{Options: map[string]string{"reasoning_summary": "invalid"}}, true},
		{"invalid effort", transcript.Config{Effort: "invalid"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts, err := recordingOptions(tt.config)
			if (err != nil) != tt.wantErr {
				t.Fatalf("error=%v, want error=%t", err, tt.wantErr)
			}
			if !tt.wantErr && len(opts) != 6 {
				t.Fatalf("options=%d, want 6", len(opts))
			}
		})
	}
}
