package codex_test

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/roomscript/coderoom/internal/agent/codex"
)

func TestCreateReplayMatch(t *testing.T) {
	tests := []struct{ name, raw, want string }{
		{"initialize", `{"id":1,"method":"initialize","params":{"clientInfo":{"name":"test","version":"volatile"},"capabilities":{"experimentalApi":true}},"extra":1}`, `{"id":1,"method":"initialize","params":{"clientInfo":{"name":"test"},"capabilities":{"experimentalApi":true}}}`},
		{"thread", `{"id":2,"method":"thread/start","params":{"model":"model","developerInstructions":"system","cwd":"/tmp"}}`, `{"id":2,"method":"thread/start","params":{"model":"model","developerInstructions":"system"}}`},
		{"turn", `{"id":3,"method":"turn/start","params":{"threadId":"t","input":[],"outputSchema":{},"model":"model","effort":"low","cwd":"/tmp"}}`, `{"id":3,"method":"turn/start","params":{"threadId":"t","input":[],"outputSchema":{},"model":"model","effort":"low"}}`},
		{"interrupt", `{"id":4,"method":"turn/interrupt","params":{"threadId":"t","turnId":"u","extra":true}}`, `{"id":4,"method":"turn/interrupt","params":{"threadId":"t","turnId":"u"}}`},
		{"other", `{"id":5,"result":{"decision":"accept"}}`, `{"id":5,"result":{"decision":"accept"}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var payload map[string]json.RawMessage
			var want map[string]json.RawMessage
			if err := json.Unmarshal([]byte(tt.raw), &payload); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal([]byte(tt.want), &want); err != nil {
				t.Fatal(err)
			}
			before := replayJSON(t, payload)
			got, err := codex.CreateReplayMatch(payload)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before, replayJSON(t, payload)) {
				t.Fatal("input mutated")
			}
			assertReplayMatchJSON(t, got, want)

		})
	}
}

func TestIsTurnActivity(t *testing.T) {
	tests := []struct {
		name, raw string
		want      bool
	}{
		{"user started", `{"method":"item/started","params":{"item":{"type":"userMessage"}}}`, true},
		{"turn started", `{"method":"turn/started","params":{"turn":{"id":"t"}}}`, false},
		{"reasoning", `{"method":"item/started","params":{"item":{"type":"reasoning"}}}`, false},
		{"completed", `{"method":"item/completed","params":{"item":{"type":"userMessage"}}}`, false},
		{"malformed", `invalid`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := codex.IsTurnActivity(tt.raw); got != tt.want {
				t.Fatalf("got %t, want %t", got, tt.want)
			}
		})
	}
}

func TestCreateReplayMatchRejectsMalformedFields(t *testing.T) {
	tests := []struct{ name, raw string }{
		{"method", `{"method":42}`},
		{"params", `{"method":"turn/start","params":[]}`},
		{"client info", `{"method":"initialize","params":{"clientInfo":false}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var message map[string]json.RawMessage
			if err := json.Unmarshal([]byte(tt.raw), &message); err != nil {
				t.Fatal(err)
			}
			if _, err := codex.CreateReplayMatch(message); err == nil {
				t.Fatal("expected malformed field error")
			}
		})
	}
}

func replayJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func assertReplayMatchJSON(t *testing.T, got, want map[string]json.RawMessage) {
	t.Helper()
	var gotValue, wantValue any
	if err := json.Unmarshal(replayJSON(t, got), &gotValue); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(replayJSON(t, want), &wantValue); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotValue, wantValue) {
		t.Fatalf("got %#v, want %#v", gotValue, wantValue)
	}
}
