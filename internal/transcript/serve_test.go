package transcript

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestServeReplay(t *testing.T) {
	steps := []Step{
		{
			Kind: "recv",
			Match: map[string]any{
				"method": "initialize",
				"id":     float64(1),
			},
		},
		{
			Kind:    "send",
			Message: map[string]any{"id": float64(1), "result": map[string]any{}},
		},
	}

	var stdout bytes.Buffer
	err := ServeReplay(strings.NewReader("{\"method\":\"initialize\",\"id\":1,\"params\":{\"extra\":true}}\n"), &stdout, steps, nil)
	if err != nil {
		t.Fatalf("ServeReplay() error = %v", err)
	}
	if got := stdout.String(); got != "{\"id\":1,\"result\":{}}\n" {
		t.Fatalf("stdout = %q, want encoded replay response", got)
	}
}

func TestServeReplayMismatch(t *testing.T) {
	steps := []Step{
		{
			Kind: "recv",
			Match: map[string]any{
				"method": "initialize",
				"id":     float64(1),
			},
		},
	}

	err := ServeReplay(strings.NewReader("{\"method\":\"thread/start\",\"id\":1}\n"), &bytes.Buffer{}, steps, nil)
	if err == nil {
		t.Fatal("ServeReplay() error = nil, want mismatch")
	}
}

func TestServeReplayBindings(t *testing.T) {
	tests := []struct {
		name             string
		recorded, actual []string
		wantErr          bool
	}{
		{"bind and reuse", []string{"old", "old"}, []string{"new", "new"}, false},
		{"rebound recorded", []string{"old", "old"}, []string{"new", "different"}, true},
		{"reused actual", []string{"old", "other"}, []string{"new", "new"}, true},
		{"empty actual", []string{"old"}, []string{""}, true},
		{"empty recorded", []string{""}, []string{"new"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var steps []Step
			var input bytes.Buffer
			for i, recorded := range tt.recorded {
				steps = append(steps, Step{Kind: "recv", Match: map[string]any{"id": recorded}})
				if err := json.NewEncoder(&input).Encode(map[string]any{"id": tt.actual[i]}); err != nil {
					t.Fatal(err)
				}
			}
			// Both a later match and send use nested values; keys and substrings stay literal.
			nested := map[string]any{"old": []any{"old", map[string]any{"text": "prefix-old"}}}
			steps = append(steps, Step{Kind: "recv", Match: nested}, Step{Kind: "send", Message: nested})
			input.WriteString("{\"old\":[\"new\",{\"text\":\"prefix-old\"}]}\n")
			bind := func(expected, actual map[string]any) (map[string]string, error) {
				recorded, ok := expected["id"].(string)
				if !ok {
					return nil, nil
				}
				return map[string]string{recorded: actual["id"].(string)}, nil
			}
			var output bytes.Buffer
			err := ServeReplay(&input, &output, steps, bind)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ServeReplay() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && output.String() != "{\"old\":[\"new\",{\"text\":\"prefix-old\"}]}\n" {
				t.Fatalf("unexpected output: %s", &output)
			}
			if nested["old"].([]any)[0] != "old" {
				t.Fatal("replay mutated fixture")
			}
		})
	}
}

func TestServeReplayLongLine(t *testing.T) {
	text := strings.Repeat("x", 128*1024)
	steps := []Step{{Kind: "recv", Match: map[string]any{"text": text}}, {Kind: "send", Message: "ok"}}
	var output bytes.Buffer
	err := ServeReplay(strings.NewReader(`{"text":"`+text+`"}`), &output, steps, nil)
	if err != nil {
		t.Fatal(err)
	}
	if output.String() != "\"ok\"\n" {
		t.Fatalf("unexpected output: %s", &output)
	}
}
