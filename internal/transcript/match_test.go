package transcript

import "testing"

func TestMatchReplayLine(t *testing.T) {
	tests := []struct {
		name     string
		expected any
		raw      string
		wantErr  bool
	}{
		{"object subset", map[string]any{"method": "request"}, `{"extra":true,"method":"request"}`, false},
		{"missing key", map[string]any{"method": "request"}, `{}`, true},
		{"nested mismatch", map[string]any{"data": map[string]any{"value": true}}, `{"data":{"value":false}}`, true},
		{"array", []any{"a", float64(1), nil}, `["a",1,null]`, false},
		{"array length", []any{"a"}, `[]`, true},
		{"wrong type", []any{}, `{}`, true},
		{"invalid JSON", nil, `invalid`, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := MatchReplayLine(tt.expected, []byte(tt.raw)); (err != nil) != tt.wantErr {
				t.Fatalf("error=%v, want error=%t", err, tt.wantErr)
			}
		})
	}
}
