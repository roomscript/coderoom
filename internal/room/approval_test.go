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
			request := agent.ApprovalRequest{Kind: agent.ApprovalFileChange, Presentation: agent.ApprovalPresentation{Evidence: "scope"}, Details: agent.ApprovalDetails{Operation: agent.FileOperation{
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
		if text := DeriveApprovalPresentation(agent.ApprovalRequest{Kind: agent.ApprovalFileChange, Details: agent.ApprovalDetails{Operation: operation}}).Evidence; !strings.Contains(text, "unavailable") {
			t.Fatalf("missing evidence notice: %q", text)
		}
	}
}

func TestFileApprovalPresentation(t *testing.T) {
	for _, evidence := range []string{"", "Grant root: /outside"} {
		t.Run(evidence, func(t *testing.T) {
			r := New(WithWorkspaceRoot("/project"))
			t.Cleanup(r.Close)
			diff := "--- /project/src/file.go\n+++ /project/src/file.go\n@@ -1 +1 @@\n-old\n+new"
			req := agent.ApprovalRequest{Kind: agent.ApprovalFileChange, Presentation: agent.ApprovalPresentation{Evidence: evidence}, Details: agent.ApprovalDetails{Operation: agent.FileOperation{
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
			if evidence != "" && !strings.Contains(text, evidence) {
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
			op := agent.CommandOperation{Command: command}
			req := agent.ApprovalRequest{Kind: agent.ApprovalCommandExecution, Presentation: agent.ApprovalPresentation{Explanation: strings.Repeat("why ", 100)}, Details: agent.ApprovalDetails{Operation: op}}
			r.ApplyEvent(session.ApprovalRequested{ID: 1, Alias: alias, Req: req})
			before := r.Snapshot()
			record := before.Records[0]
			for _, want := range []string{command, req.Presentation.Explanation, "(cwd: unknown)"} {
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
		text := (&Room{}).approvalText(agent.ApprovalRequest{Kind: agent.ApprovalCommandExecution, Details: agent.ApprovalDetails{Operation: op}})
		if !strings.Contains(text, "unavailable") || !strings.Contains(text, "unknown") {
			t.Fatalf("missing notice: %q", text)
		}
	}
}

func TestCommandApprovalHeadingCwd(t *testing.T) {
	for _, cwd := range []string{"", "/full/path", "/" + strings.Repeat("long-path/", 100) + "界\x1b[2J"} {
		r := New()
		t.Cleanup(r.Close)
		req := agent.ApprovalRequest{Kind: agent.ApprovalCommandExecution, Presentation: agent.ApprovalPresentation{Explanation: "full\ncontext"}, Details: agent.ApprovalDetails{Operation: agent.CommandOperation{Command: "echo hello", Cwd: cwd}}}
		r.ApplyEvent(session.ApprovalRequested{ID: 1, Req: req})
		want := cwd
		if want == "" {
			want = "unknown"
		}
		text := r.Snapshot().Records[0].Text
		if !strings.HasPrefix(text, "⚡ Approval: echo (cwd: "+want+")\n") || strings.Contains(text, "Working directory:") || !strings.Contains(text, "Explanation: full\ncontext") {
			t.Fatalf("bad evidence: %q", text)
		}
	}
}

func TestPermissionApprovalRecords(t *testing.T) {
	for _, summary := range []string{"", "Tool: Read", "Plan approval", "supplied legacy context"} {
		for _, choice := range []agent.ApprovalOption{agent.OptionAccept, agent.OptionDecline, ""} {
			r := New()
			t.Cleanup(r.Close)
			alias := newTestAlias("bill")
			evidence := strings.Repeat("full input\n", 100) + "tail\x1b[2J"
			req := agent.ApprovalRequest{Kind: agent.ApprovalPermissions, Presentation: agent.ApprovalPresentation{Summary: summary, Explanation: "full reason", Evidence: evidence}, Details: agent.ApprovalDetails{Operation: agent.FileOperation{Paths: []string{"/file"}}}}
			for _, id := range []int64{1, 2} {
				r.ApplyEvent(session.ApprovalRequested{ID: id, Alias: alias, Req: req})
				r.ApplyEvent(session.ApprovalCleared{ID: id, Alias: alias, Choice: choice})
			}
			records := r.Snapshot().Records
			if len(records) != 2 {
				t.Fatalf("records = %v", records)
			}
			assertPermissionRecords(t, records, req, choice)
		}
	}
	for _, ask := range []string{"", "Permission request"} {
		req := agent.ApprovalRequest{Kind: agent.ApprovalPermissions, Presentation: agent.ApprovalPresentation{Summary: ask}, Details: agent.ApprovalDetails{Raw: []byte(`{"hidden":"metadata"}`)}}
		text := (&Room{}).approvalText(req)
		if strings.Count(text, "Permission request") != 1 || !strings.Contains(text, "Request evidence unavailable.") || strings.Contains(text, "metadata") {
			t.Fatalf("fallback = %q", text)
		}
	}
}

func assertPermissionRecords(t *testing.T, records []Record, req agent.ApprovalRequest, choice agent.ApprovalOption) {
	t.Helper()
	summary := req.Presentation.Summary
	if summary == "" {
		summary = "Permission request"
	}
	for _, record := range records {
		for _, want := range []string{"⚡ Approval: " + summary, req.Presentation.Summary, req.Presentation.Explanation, req.Presentation.Evidence} {
			if !strings.Contains(record.Text, want) {
				t.Fatalf("lost %q", want)
			}
		}
		if record.Decision != approvalDecision(choice) {
			t.Fatal("lost decision")
		}
	}
}

func TestPermissionHistoryDoesNotRepeatExplanation(t *testing.T) {
	for _, evidence := range []string{"", "Requested permissions: scope"} {
		req := agent.ApprovalRequest{Kind: agent.ApprovalPermissions, Presentation: agent.ApprovalPresentation{Summary: "Additional permissions", Explanation: "full supplied reason", Evidence: evidence}}
		text := (&Room{}).approvalText(req)
		if strings.Count(text, req.Presentation.Explanation) != 1 || strings.Count(text, req.Presentation.Summary) != 1 {
			t.Fatalf("repeated or lost context: %q", text)
		}
	}
}

func TestApprovalPresentationComposition(t *testing.T) {
	for _, kind := range []agent.ApprovalKind{agent.ApprovalCommandExecution, agent.ApprovalFileChange, agent.ApprovalPermissions, "future"} {
		for _, evidence := range []string{"", "complete supplemental evidence\ntail\x1b[2J"} {
			reason := "  full supplied reason\nsecond line 界\x1b[2J "
			req := agent.ApprovalRequest{Kind: kind, Presentation: agent.ApprovalPresentation{Explanation: reason, Evidence: evidence}}
			derived := DeriveApprovalPresentation(req)
			text := (&Room{}).approvalText(req)
			if derived.Summary == "" || derived.Explanation != reason || strings.Count(text, reason) != 1 || strings.Contains(derived.Evidence, reason) {
				t.Fatalf("inconsistent composition: %+v, %q", derived, text)
			}
			if !strings.Contains(text, derived.Evidence) || !strings.Contains(derived.Evidence, evidence) {
				t.Fatal("supplemental evidence lost")
			}
		}
	}
}

func TestApprovalPresentationDetachesPaths(t *testing.T) {
	req := agent.ApprovalRequest{Kind: agent.ApprovalFileChange, Details: agent.ApprovalDetails{Operation: agent.FileOperation{Paths: []string{"/project/file"}}}}
	r := New(WithWorkspaceRoot("/project"))
	t.Cleanup(r.Close)
	first := r.DeriveApprovalPresentation(req)
	first.Paths[0] = "changed"
	if next := r.DeriveApprovalPresentation(req); next.Paths[0] != "file" || req.Details.Operation.(agent.FileOperation).Paths[0] != "/project/file" {
		t.Fatal("derived paths alias operation data")
	}
}
