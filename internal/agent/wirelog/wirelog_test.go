package wirelog

import (
	"bytes"
	"strings"
	"testing"
)

func TestObserver(t *testing.T) {
	var buf bytes.Buffer
	o := New(&buf, "ada")
	o.OnSend(`{"a":1}`)
	o.OnReceive(`{"b":2}`)

	lines := strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
	want := []string{`ada → {"a":1}`, `ada ← {"b":2}`}
	if len(lines) != len(want) {
		t.Fatalf("lines = %q, want %d", lines, len(want))
	}
	for i, line := range lines {
		ts, rest, _ := strings.Cut(line, " ")
		if ts == "" || rest != want[i] {
			t.Errorf("line %d = %q, want a timestamp then %q", i, line, want[i])
		}
	}
}
