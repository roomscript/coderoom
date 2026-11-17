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
