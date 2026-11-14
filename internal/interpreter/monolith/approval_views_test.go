package monolith

import (
	"strings"
	"testing"

	"github.com/roomscript/coderoom/internal/agent"
	"github.com/roomscript/coderoom/internal/participant"
)

func TestFileApprovalDisplayEvidence(t *testing.T) {
	changes := []agent.FileChange{
		{Path: "/project/" + strings.Repeat("long", 100) + "\nname", ChangeKind: "add", Diff: "+first\n+second\n\x1b[2J\n"},
		{Path: "/project/old", ChangeKind: "delete", Diff: "-old\n"},
	}
	for _, available := range []bool{true, false} {
		req := agent.ApprovalRequest{Kind: agent.ApprovalFileChange, Details: agent.ApprovalDetails{Backend: "codex", Raw: []byte(`{"itemId":"secret","startedAtMs":123,"reason":null}`)}}
		if available {
			req.Details.Operation = agent.FileOperation{Write: true, ProposedChanges: changes}
		}
		view := approvalFromAgent(1, participant.Alias{Name: "bill"}, req)
		if strings.Contains(view.Details, "secret") || strings.Contains(view.Details, "null") || strings.Contains(view.Details, "123") {
			t.Fatalf("raw metadata displayed: %s", view.Details)
		}
		if !available {
			if !strings.Contains(view.Details, "no matching file-change evidence") {
				t.Fatal("missing evidence not reported")
			}
			continue
		}
		for _, change := range changes {
			if !strings.Contains(view.Details, change.ChangeKind+": "+change.Path+"\n"+change.Diff) {
				t.Fatal("display evidence shortened or lost")
			}
		}
	}
}

func TestApprovalDisplayFallbackEvidence(t *testing.T) {
	evidence := "Full scope or plan\n" + strings.Repeat("details\n", 100) + "tail\x1b[2J"
	for _, backend := range []string{"codex", "claude"} {
		for _, operation := range []agent.Operation{nil, agent.HTTPOperation{Method: "GET"}} {
			req := agent.ApprovalRequest{Kind: agent.ApprovalPermissions, Ask: "Short summary", Details: agent.ApprovalDetails{Backend: backend, Operation: operation, Display: evidence, Raw: []byte(`{"requestId":"hidden"}`)}}
			view := approvalFromAgent(1, participant.Alias{Name: "bill"}, req)
			if view.Details != evidence || view.Prompt != req.Ask {
				t.Fatalf("view = %+v", view)
			}
		}
	}
}

func TestApprovalDisplayUsesCommonOperations(t *testing.T) {
	command := "echo " + strings.Repeat("long", 100) + "\nsecond command"
	for _, backend := range []string{"codex", "claude"} {
		for _, tt := range []struct {
			name string
			op   agent.Operation
			want string
		}{
			{"command", agent.CommandOperation{Command: command, Cwd: "/project"}, "Command: " + command + "\nWorking directory: /project"},
			{"file", agent.FileOperation{Write: true, ProposedChanges: []agent.FileChange{{Path: "/project/file", ChangeKind: "update", Diff: "-old\n+new\n"}}}, "update: /project/file\n-old\n+new\n"},
			{"missing patch", agent.FileOperation{Write: true, Paths: []string{"/project/file"}}, "/project/file\nProposed file details unavailable: no matching file-change evidence."},
			{"unsupported", nil, ""},
		} {
			t.Run(backend+"/"+tt.name, func(t *testing.T) {
				req := agent.ApprovalRequest{Ask: "Existing readable prompt", Options: []agent.ApprovalOption{agent.OptionDecline}, Details: agent.ApprovalDetails{Backend: backend, Operation: tt.op, Raw: []byte(`{"id":"private","timestamp":123,"unused":null}`)}}
				view := approvalFromAgent(1, participant.Alias{Name: "bill"}, req)
				if view.Prompt != req.Ask || view.Details != tt.want || view.Options[0].ID != string(agent.OptionDecline) {
					t.Fatalf("view = %+v", view)
				}
			})
		}
	}
}
