package room

import (
	"strings"
	"testing"

	"github.com/roomscript/coderoom/internal/agent"
	"github.com/roomscript/coderoom/internal/session"
)

func TestFileApprovalRecordSettlement(t *testing.T) {
	for _, choice := range []agent.ApprovalOption{agent.OptionAccept, agent.OptionAcceptForSession, agent.OptionDecline, agent.OptionCancel, ""} {
		t.Run(string(choice), func(t *testing.T) {
			r := New()
			t.Cleanup(r.Close)
			alias := newTestAlias("bill")
			r.ApplyEvent(session.AgentReady{Alias: alias})
			request := agent.ApprovalRequest{Kind: agent.ApprovalFileChange, Ask: "scope", Details: agent.ApprovalDetails{Operation: agent.FileOperation{
				Paths: []string{"first", "second"}, ProposedChanges: []agent.FileChange{{Path: "first", ChangeKind: "update", Diff: strings.Repeat("+full evidence\n", 100) + "+tail"}},
			}}}
			r.ApplyEvent(session.ApprovalRequested{Alias: alias, ID: 42, Req: request})
			before := r.Snapshot()
			record := before.Records[len(before.Records)-1]
			assertApprovalEvidence(t, record)
			if _, ok := r.LatestCompletedOutput(alias); ok {
				t.Fatal("approval entered handoff output")
			}
			r.ApplyEvent(session.ApprovalCleared{Alias: alias, ID: 99, Choice: choice})
			r.ApplyEvent(session.ApprovalCleared{Alias: newTestAlias("bill"), ID: 42, Choice: choice})
			if r.Snapshot().Version != before.Version {
				t.Fatal("stale identity updated record")
			}
			r.ApplyEvent(session.ApprovalCleared{Alias: alias, ID: 42, Choice: choice})
			after := r.Snapshot()
			updated := after.Records[len(after.Records)-1]
			if len(after.Records) != len(before.Records) || updated.Text != record.Text || updated.Decision != approvalDecision(choice) || record.Decision != "Waiting for you" {
				t.Fatalf("settlement changed evidence or snapshot: %#v", updated)
			}
		})
	}
}

func assertApprovalEvidence(t *testing.T, record Record) {
	t.Helper()
	if record.Kind != KindApproval || record.Alias.Name != "bill" || record.Msg != nil || record.HandoffSource || !strings.Contains(record.Text, "+tail") || !strings.Contains(record.Text, "second") {
		t.Fatalf("record = %#v", record)
	}
	if !strings.Contains(record.Text, "second\nProposed file details unavailable: no matching file-change evidence.") {
		t.Fatal("uncovered file has no unavailable-evidence notice")
	}
}

func TestFileApprovalMissingEvidence(t *testing.T) {
	for _, operation := range []agent.Operation{nil, agent.FileOperation{Paths: []string{"file"}}, agent.FileOperation{ProposedChanges: []agent.FileChange{{Path: "file"}}}} {
		if text := ApprovalDetails(agent.ApprovalRequest{Kind: agent.ApprovalFileChange, Details: agent.ApprovalDetails{Operation: operation}}); !strings.Contains(text, "unavailable") {
			t.Fatalf("missing evidence notice: %q", text)
		}
	}
}

func TestFileApprovalPresentation(t *testing.T) {
	for _, ask := range []string{"approve file changes", "approve file changes — required for migration", "approve file changes (grantRoot: /outside)"} {
		t.Run(ask, func(t *testing.T) {
			r := New(WithWorkspaceRoot("/project"))
			t.Cleanup(r.Close)
			diff := "--- /project/src/file.go\n+++ /project/src/file.go\n@@ -1 +1 @@\n-old\n+new"
			req := agent.ApprovalRequest{Kind: agent.ApprovalFileChange, Ask: ask, Details: agent.ApprovalDetails{Operation: agent.FileOperation{
				Paths:           []string{"/project/src/file.go", "/project-other/file.go", "/outside/file.go"},
				ProposedChanges: []agent.FileChange{{Path: "/project/src/file.go", ChangeKind: "update", Diff: diff}},
			}}}
			r.ApplyEvent(session.ApprovalRequested{ID: 1, Req: req})
			text := r.Snapshot().Records[0].Text
			for _, want := range []string{"Files:\nsrc/file.go\n/project-other/file.go\n/outside/file.go", "update: src/file.go\n" + diff, "Proposed file details unavailable"} {
				if !strings.Contains(text, want) {
					t.Fatalf("missing %q in %q", want, text)
				}
			}
			if strings.Contains(text, ask) != (ask != "approve file changes") {
				t.Fatalf("incorrect description: %q", text)
			}
			if paths := r.ApprovalPaths(req); strings.Join(paths, "\n") != "src/file.go\n/project-other/file.go\n/outside/file.go" {
				t.Fatalf("display paths = %v", paths)
			}
			if req.Details.Operation.(agent.FileOperation).Paths[0] != "/project/src/file.go" {
				t.Fatal("request target mutated")
			}
		})
	}
}

func TestCommandApprovalRecordSettlement(t *testing.T) {
	for _, choice := range []agent.ApprovalOption{agent.OptionAccept, agent.OptionAcceptForSession, agent.OptionDecline, agent.OptionCancel, ""} {
		t.Run(string(choice), func(t *testing.T) {
			r := New()
			t.Cleanup(r.Close)
			alias := newTestAlias("bill")
			command := "/opt/bin/git diff " + strings.Repeat("long argument\n", 100) + "tail\x1b[2J"
			op := agent.CommandOperation{Command: command, Explanation: strings.Repeat("why ", 100)}
			req := agent.ApprovalRequest{Kind: agent.ApprovalCommandExecution, Ask: "shortened", Details: agent.ApprovalDetails{Operation: op}}
			r.ApplyEvent(session.ApprovalRequested{ID: 1, Alias: alias, Req: req})
			before := r.Snapshot()
			record := before.Records[0]
			for _, want := range []string{command, op.Explanation, "Working directory: unknown"} {
				if !strings.Contains(record.Text, want) {
					t.Fatalf("lost %q", want)
				}
			}
			r.ApplyEvent(session.ApprovalCleared{ID: 99, Alias: alias, Choice: choice})
			if r.Snapshot().Version != before.Version {
				t.Fatal("stale settlement accepted")
			}
			r.ApplyEvent(session.ApprovalCleared{ID: 1, Alias: alias, Choice: choice})
			after := r.Snapshot()
			if len(after.Records) != 1 || after.Records[0].Text != record.Text || after.Records[0].Decision != approvalDecision(choice) {
				t.Fatal("lost evidence or settlement")
			}
			if req.Details.Operation != op {
				t.Fatal("operation mutated")
			}
		})
	}
}

func TestCommandApprovalMissingEvidence(t *testing.T) {
	for _, op := range []agent.Operation{nil, agent.CommandOperation{}} {
		text := ApprovalDetails(agent.ApprovalRequest{Kind: agent.ApprovalCommandExecution, Details: agent.ApprovalDetails{Operation: op}})
		if !strings.Contains(text, "unavailable") || !strings.Contains(text, "unknown") {
			t.Fatalf("missing notice: %q", text)
		}
	}
}
