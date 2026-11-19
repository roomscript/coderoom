package monolith

import (
	"strings"
	"testing"

	"github.com/roomscript/coderoom/internal/agent"
	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/room"
	"github.com/roomscript/coderoom/internal/session"
)

func TestActiveFileApprovalUsesWorkspacePaths(t *testing.T) {
	model := newInterpreterModel(room.WithWorkspaceRoot("/project"))
	t.Cleanup(model.Close)
	longName := strings.Repeat("long/", 80) + "file.go"
	event := session.ApprovalRequested{ID: 1, Req: agent.ApprovalRequest{
		Kind: agent.ApprovalFileChange,
		Details: agent.ApprovalDetails{Operation: agent.FileOperation{
			Paths: []string{"/project/" + longName, "/outside/file.go"},
		}},
	}}
	model.applyApprovalEvent(event)
	model.room.ApplyEvent(event)
	paths := model.approval.Paths
	if len(paths) != 2 || paths[0] != longName || paths[1] != "/outside/file.go" {
		t.Fatalf("compact paths = %v", paths)
	}
	if !strings.Contains(model.room.Snapshot().Records[0].Text, "Files:\n"+strings.Join(paths, "\n")) {
		t.Fatal("history and compact paths differ")
	}
}

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
		view := approvalFromAgent(1, participant.Alias{Name: "bill"}, req, room.DeriveApprovalPresentation(req))
		if strings.Contains(view.Evidence, "secret") || strings.Contains(view.Evidence, "null") || strings.Contains(view.Evidence, "123") {
			t.Fatalf("raw metadata displayed: %s", view.Evidence)
		}
		if !available {
			if !strings.Contains(view.Evidence, "no matching file-change evidence") {
				t.Fatal("missing evidence not reported")
			}
			continue
		}
		for _, change := range changes {
			if !strings.Contains(view.Evidence, change.ChangeKind+": "+change.Path+"\n"+change.Diff) {
				t.Fatal("display evidence shortened or lost")
			}
		}
	}
}

func TestApprovalDisplayFallbackEvidence(t *testing.T) {
	evidence := "Full scope or plan\n" + strings.Repeat("details\n", 100) + "tail\x1b[2J"
	for _, backend := range []string{"codex", "claude"} {
		for _, operation := range []agent.Operation{nil, agent.HTTPOperation{Method: "GET"}} {
			req := agent.ApprovalRequest{Kind: agent.ApprovalPermissions, Presentation: agent.ApprovalPresentation{Summary: "Short summary", Evidence: evidence}, Details: agent.ApprovalDetails{Backend: backend, Operation: operation, Raw: []byte(`{"requestId":"hidden"}`)}}
			view := approvalFromAgent(1, participant.Alias{Name: "bill"}, req, room.DeriveApprovalPresentation(req))
			if view.Evidence != evidence || view.Summary != req.Presentation.Summary {
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
			kind agent.ApprovalKind
			op   agent.Operation
			want string
		}{
			{"command", agent.ApprovalCommandExecution, agent.CommandOperation{Command: command, Cwd: "/project"}, "Command: " + command},
			{"file", agent.ApprovalFileChange, agent.FileOperation{Write: true, ProposedChanges: []agent.FileChange{{Path: "/project/file", ChangeKind: "update", Diff: "-old\n+new\n"}}}, "update: /project/file\n-old\n+new\n"},
			{"missing patch", agent.ApprovalFileChange, agent.FileOperation{Write: true, Paths: []string{"/project/file"}}, "/project/file\nProposed file details unavailable: no matching file-change evidence."},
			{"unsupported", "", nil, "Request evidence unavailable."},
		} {
			t.Run(backend+"/"+tt.name, func(t *testing.T) {
				req := agent.ApprovalRequest{Kind: tt.kind, Options: []agent.ApprovalOption{agent.OptionDecline}, Details: agent.ApprovalDetails{Backend: backend, Operation: tt.op, Raw: []byte(`{"id":"private","timestamp":123,"unused":null}`)}}
				view := approvalFromAgent(1, participant.Alias{Name: "bill"}, req, room.DeriveApprovalPresentation(req))
				if !strings.Contains(view.Evidence, tt.want) || view.Options[0].ID != string(agent.OptionDecline) {
					t.Fatalf("view = %+v", view)
				}
			})
		}
	}
}

func TestCommandExplanationProjection(t *testing.T) {
	for _, explanation := range []string{"", "  full context\n界\x1b[2J"} {
		req := agent.ApprovalRequest{Kind: agent.ApprovalCommandExecution, Presentation: agent.ApprovalPresentation{Explanation: explanation}}
		if got := approvalFromAgent(1, participant.Alias{}, req, room.DeriveApprovalPresentation(req)); got.Explanation != explanation {
			t.Fatalf("explanation = %q", got.Explanation)
		}
	}
}

func TestPermissionProjection(t *testing.T) {
	for _, summary := range []string{"", "Plan approval", "Tool: Read"} {
		req := agent.ApprovalRequest{Kind: agent.ApprovalPermissions, Presentation: agent.ApprovalPresentation{Summary: summary, Explanation: "  supplied\ncontext", Evidence: "full input"}, Options: []agent.ApprovalOption{agent.OptionAccept, agent.OptionDecline}}
		got := approvalFromAgent(42, participant.Alias{Name: "bill"}, req, room.DeriveApprovalPresentation(req))
		if got.Summary != room.DeriveApprovalPresentation(req).Summary || got.Explanation != req.Presentation.Explanation || got.Evidence != req.Presentation.Evidence || got.ID != 42 {
			t.Fatalf("projection = %+v", got)
		}
		got.Options[0].ID = "changed"
		if req.Options[0] != agent.OptionAccept {
			t.Fatal("options aliased")
		}
	}
}
