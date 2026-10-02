package interpreter

import (
	"testing"

	"github.com/trigosec/coderoom/internal/agent"
	roomstate "github.com/trigosec/coderoom/internal/room"
)

func TestRecordDetails_ignoreUnrelatedContent(t *testing.T) {
	tests := []struct {
		name   string
		record roomstate.Record
	}{
		{name: "no message", record: roomstate.Record{Kind: roomstate.KindSystem}},
		{name: "output", record: roomstate.NewAgentRecord("ada", agent.Message{Content: agent.Output{Text: "hello"}})},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, ok := CommandFromRecord(test.record); ok {
				t.Fatal("unexpected command details")
			}
			if _, ok := FileChangesFromRecord(test.record); ok {
				t.Fatal("unexpected file-change details")
			}
		})
	}
}

func TestCommandFromRecord_detachesExitCode(t *testing.T) {
	code := 7
	record := roomstate.NewAgentRecord("ada", agent.Message{Content: agent.Command{Command: "go test", Cwd: "/repo", Output: "failed", ExitCode: &code}})
	details, ok := CommandFromRecord(record)
	if !ok || details.Command != "go test" || details.Cwd != "/repo" || details.Output != "failed" || details.ExitCode == nil || *details.ExitCode != 7 {
		t.Fatalf("details = %#v, ok = %v", details, ok)
	}
	*details.ExitCode = 0
	if code != 7 {
		t.Fatal("presentation mutation changed canonical exit code")
	}
}

func TestFileChangesFromRecord_detachesChanges(t *testing.T) {
	changes := []agent.FileChange{{Path: "main.go", Diff: "+hello", ChangeKind: "update"}}
	record := roomstate.NewAgentRecord("ada", agent.Message{Content: agent.FileChangeSet{Status: agent.ToolStatusCompleted, Changes: changes}})
	details, ok := FileChangesFromRecord(record)
	if !ok || details.Status != "completed" || len(details.Changes) != 1 || details.Changes[0] != (FileChange{Path: "main.go", Diff: "+hello", ChangeKind: "update"}) {
		t.Fatalf("details = %#v, ok = %v", details, ok)
	}
	details.Changes[0].Path = "changed.go"
	if changes[0].Path != "main.go" {
		t.Fatal("presentation mutation changed canonical file changes")
	}
}
