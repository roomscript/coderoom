package claude

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/roomscript/coderoom/internal/agent"
)

func TestBindReplayIDs(t *testing.T) {
	tests := []struct {
		name, expected, actual string
		want                   map[string]string
	}{
		{"user prompt", `{"type":"user","uuid":"recorded","message":{"content":"hello"}}`, `{"type":"user","uuid":"fresh","message":{"content":"hello"}}`, map[string]string{"recorded": "fresh"}},
		{"control response", `{"type":"control_response","uuid":"recorded"}`, `{"type":"control_response","uuid":"fresh"}`, nil},
		{"user without uuid", `{"type":"user"}`, `{"type":"user","uuid":"fresh"}`, nil},
		{"actual without uuid", `{"type":"user","uuid":"recorded"}`, `{"type":"user"}`, nil},
		{"nonstring recorded uuid", `{"type":"user","uuid":42}`, `{"type":"user","uuid":"fresh"}`, nil},
		{"nonstring actual uuid", `{"type":"user","uuid":"recorded"}`, `{"type":"user","uuid":42}`, nil},
		{"missing type", `{"uuid":"recorded"}`, `{"uuid":"fresh"}`, nil},
		// The shared replay peer validates empty values and one-to-one bindings.
		{"empty recorded uuid", `{"type":"user","uuid":""}`, `{"type":"user","uuid":"fresh"}`, map[string]string{"": "fresh"}},
		{"empty actual uuid", `{"type":"user","uuid":"recorded"}`, `{"type":"user","uuid":""}`, map[string]string{"recorded": ""}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var expected, actual map[string]any
			if err := json.Unmarshal([]byte(tt.expected), &expected); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal([]byte(tt.actual), &actual); err != nil {
				t.Fatal(err)
			}
			got, err := BindReplayIDs(expected, actual)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("bindings=%v, want %v", got, tt.want)
			}
			for _, input := range []struct {
				got      map[string]any
				original string
			}{{expected, tt.expected}, {actual, tt.actual}} {
				var original map[string]any
				if err := json.Unmarshal([]byte(input.original), &original); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(input.got, original) {
					t.Fatal("binding mutated input")
				}
			}
		})
	}
}

func TestIsStderrLogStream(t *testing.T) {
	for _, tt := range []struct {
		id   agent.StreamID
		want bool
	}{
		{stderrStreamID, true}, {logStreamID, false}, {turnStreamID, false}, {"", false},
	} {
		t.Run(string(tt.id), func(t *testing.T) {
			if got := IsStderrLogStream(tt.id); got != tt.want {
				t.Fatalf("IsStderrLogStream(%q)=%t, want %t", tt.id, got, tt.want)
			}
		})
	}
}
