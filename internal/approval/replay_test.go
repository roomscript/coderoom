package approval

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/roomscript/coderoom/internal/agent"
)

// Replay is opt-in and emits only record numbers, decisions and stable reasons.
// Private evidence stays in the caller-supplied file outside the repository.
func TestReplay(t *testing.T) {
	path := os.Getenv("CODEROOM_APPROVAL_REPLAY")
	root := os.Getenv("CODEROOM_APPROVAL_REPLAY_ROOT")
	if path == "" || root == "" {
		t.Skip("private replay not configured")
	}
	f, err := os.Open(filepath.Clean(path)) //nolint:gosec // explicitly configured private, read-only replay input
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close() //nolint:errcheck // read-only replay input
	d := json.NewDecoder(io.LimitReader(f, 32*1024*1024))
	if _, err := d.Token(); err != nil {
		t.Fatal(err)
	}
	p := Policy{ProjectRoot: root, Ignored: CheckIgnored(root)}
	outcomes := map[Outcome]int{}
	counts := map[string]int{}
	for d.More() {
		var record struct {
			Line    int
			Request json.RawMessage
		}
		if err := d.Decode(&record); err != nil {
			t.Fatal(err)
		}
		e := replayEvidence(record.Request, root)
		result := p.Evaluate(context.Background(), e.Operation)
		counts[result.Reason]++
		outcomes[result.Outcome]++
		if result.Outcome == Approve {
			t.Logf("accept line=%d reason=%s", record.Line, result.Reason)
		}
	}
	if _, err := d.Token(); err != nil {
		t.Fatal(err)
	}
	t.Logf("outcomes=%v reasons=%v", outcomes, counts)
}

func replayEvidence(raw json.RawMessage, root string) agent.ApprovalDetails {
	var r struct {
		Tool    string `json:"tool_name"`
		Command string `json:"command"`
		Cwd     string `json:"cwd"`
		Input   struct {
			Command string `json:"command"`
			Path    string `json:"file_path"`
			Cwd     string `json:"cwd"`
		} `json:"input"`
	}
	_ = json.Unmarshal(raw, &r)
	details := agent.ApprovalDetails{Raw: raw}
	if r.Command != "" {
		details.Operation = agent.CommandOperation{Command: r.Command, Cwd: r.Cwd}
	}
	if r.Tool == "Bash" {
		details.Operation = agent.CommandOperation{Command: r.Input.Command, Cwd: r.Cwd}
		if r.Input.Cwd != "" {
			details.Operation = agent.CommandOperation{Command: r.Input.Command, Cwd: r.Input.Cwd}
		}
	}
	if r.Tool == "Write" || r.Tool == "Edit" {
		details.Operation = agent.FileOperation{Paths: []string{r.Input.Path}, Write: true, AllTargetsKnown: true, Cwd: root}
	}
	return details
}
