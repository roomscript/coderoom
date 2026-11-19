package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/roomscript/coderoom/internal/agent"
)

func TestCommandApprovalPreservesEvidence(t *testing.T) {
	command := "echo " + strings.Repeat("x", 300)
	raw, err := json.Marshal(map[string]any{"command": command, "cwd": "/project", "reason": "permission expansion", "unknown": map[string]bool{"host": true}})
	if err != nil {
		t.Fatal(err)
	}
	req, err := normalizeCommandApproval(raw)
	if err != nil {
		t.Fatal(err)
	}
	commandOperation, ok := req.Details.Operation.(agent.CommandOperation)
	if !ok {
		t.Fatal("expected command operation")
	}
	if commandOperation.Command != command || commandOperation.Cwd != "/project" || !bytes.Equal(req.Details.Raw, raw) {
		t.Fatal("structured evidence was lost or shortened")
	}
	if req.Presentation.Summary != "" {
		t.Fatal("command summary must be derived by room")
	}
}

func TestFileChangeApprovalPreservesFullScopeAndReason(t *testing.T) {
	root := "/project/" + strings.Repeat("long-directory/", 30) + "target with trailing space "
	reason := strings.Repeat("Complete permission reason\n", 30) + "final detail\x1b[2J "
	raw, err := json.Marshal(map[string]string{"grantRoot": root, "reason": reason})
	if err != nil {
		t.Fatal(err)
	}
	req, err := normalizeFileChangeApproval(raw)
	if err != nil {
		t.Fatal(err)
	}
	if req.Presentation.Summary != "" || req.Presentation.Explanation != reason || req.Presentation.Evidence != "Grant root: "+root {
		t.Fatalf("scope or reason lost: %+v", req.Presentation)
	}
	if !bytes.Equal(req.Details.Raw, raw) {
		t.Fatal("raw evidence changed")
	}
}

type cancelingApprovalListener struct{ cancel context.CancelFunc }

func (l cancelingApprovalListener) Decide(context.Context, agent.ApprovalRequest) (agent.ApprovalDecision, error) {
	l.cancel()
	return agent.ApprovalDecision{Choice: agent.OptionAccept}, nil
}

func TestCanceledApprovalDoesNotWriteResponse(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := New(t.TempDir(), WithApprovalListener(cancelingApprovalListener{cancel: cancel}))
	// No process or writer is installed: writing a stale response would fail.
	err := handleApprovalRequest(ctx, c, approvalRequest{req: agent.ApprovalRequest{Options: []agent.ApprovalOption{agent.OptionAccept}}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled response error = %v", err)
	}
}

func TestPermissionExpansionsDoNotInferValidatedTargets(t *testing.T) {
	for _, tt := range []struct{ method, raw string }{
		{methodFileChangeRequestApproval, `{"grantRoot":"/project","reason":"write access"}`},
		{methodPermissionsRequestApproval, `{"cwd":"/project","permissions":{"network":{}}}`},
	} {
		t.Run(tt.method, func(t *testing.T) {
			req, _, err := normalizeApproval(tt.method, json.RawMessage(tt.raw))
			if err != nil {
				t.Fatal(err)
			}
			if tt.method == methodFileChangeRequestApproval {
				assertUnvalidatedFileOperation(t, req.Details.Operation)
			} else if req.Details.Operation != nil {
				t.Fatal("permission expansion inferred a concrete operation")
			}
			if req.Details.Backend != "codex" || req.Details.Tool != tt.method || string(req.Details.Raw) != tt.raw {
				t.Fatal("permission evidence lost")
			}
		})
	}
}

func assertUnvalidatedFileOperation(t *testing.T, operation agent.Operation) {
	t.Helper()
	op, ok := operation.(agent.FileOperation)
	if !ok || !op.Write || op.AllTargetsKnown || len(op.Paths) != 0 {
		t.Fatal("file approval inferred validated targets")
	}
}

func TestPermissionsApprovalRetainsFullDisplayScope(t *testing.T) {
	reason := strings.Repeat("full reason\n", 40)
	scope := json.RawMessage(`{"fileSystem":{"write":["/outside/project/target"]},"network":{"allowedHosts":["specific.example"]}}`)
	raw, err := json.Marshal(map[string]any{"permissions": scope, "reason": reason, "cwd": "/outside/project", "threadId": "hidden-id", "startedAtMs": 123})
	if err != nil {
		t.Fatal(err)
	}
	req, perms, err := normalizePermissionsApproval(raw)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"/outside/project/target", "specific.example", "Working directory: /outside/project"} {
		if !strings.Contains(req.Presentation.Evidence, want) {
			t.Fatalf("missing %q in %s", want, req.Presentation.Evidence)
		}
	}
	if strings.Contains(req.Presentation.Evidence, "hidden-id") || strings.Contains(req.Presentation.Evidence, "startedAtMs") || req.Details.Operation != nil {
		t.Fatal("scope included protocol metadata or policy eligibility")
	}
	if !bytes.Equal(req.Details.Raw, raw) || !bytes.Equal(perms, scope) {
		t.Fatal("raw scope or response evidence changed")
	}
}

func TestCommandApprovalRetainsExplanation(t *testing.T) {
	reason := strings.Repeat("supplied explanation ", 100)
	raw, err := json.Marshal(map[string]string{"command": "/opt/bin/git diff", "cwd": "/real/cwd", "reason": reason})
	if err != nil {
		t.Fatal(err)
	}
	req, err := normalizeCommandApproval(raw)
	if err != nil {
		t.Fatal(err)
	}
	op := req.Details.Operation.(agent.CommandOperation)
	if req.Presentation.Explanation != reason || op.Command != "/opt/bin/git diff" || op.Cwd != "/real/cwd" {
		t.Fatalf("lost evidence: %+v", op)
	}
}

func TestPermissionPresentation(t *testing.T) {
	for _, tt := range []struct{ scope, summary string }{
		{`{"fileSystem":{}}`, "Additional permissions: filesystem"},
		{`{"network":{}}`, "Additional permissions: network"},
		{`{"fileSystem":{},"network":{}}`, "Additional permissions: filesystem+network"},
		{`{"future":{"unknown":"complete scope"}}`, "Additional permissions"},
		{`null`, "Additional permissions"},
	} {
		t.Run(tt.scope, func(t *testing.T) {
			reason := "  " + strings.Repeat("full reason\n", 100) + "tail\x1b[2J "
			raw, err := json.Marshal(map[string]any{"permissions": json.RawMessage(tt.scope), "reason": reason, "cwd": nil})
			if err != nil {
				t.Fatal(err)
			}
			req, _, err := normalizePermissionsApproval(raw)
			if err != nil {
				t.Fatal(err)
			}
			assertPermissionPresentation(t, req, tt.summary, reason)
			if tt.scope == "null" && !strings.Contains(req.Presentation.Evidence, "unavailable") {
				t.Fatal("missing scope notice")
			}
			if strings.Contains(tt.scope, "future") && !strings.Contains(req.Presentation.Evidence, "complete scope") {
				t.Fatal("unknown fields lost")
			}
		})
	}
	req, _, err := normalizePermissionsApproval(json.RawMessage(`{}`))
	if err != nil || !strings.Contains(req.Presentation.Evidence, "unavailable") {
		t.Fatal("missing scope notice")
	}
}

func assertPermissionPresentation(t *testing.T, req agent.ApprovalRequest, summary, reason string) {
	t.Helper()
	if agent.ApprovalPrompt(req) != summary+" — "+reason {
		t.Fatal("permission prompt was independently composed")
	}
	if req.Presentation.Summary != summary || req.Presentation.Explanation != reason || strings.Contains(req.Presentation.Evidence, reason) || !strings.Contains(req.Presentation.Evidence, "Working directory: unknown") {
		t.Fatalf("lost presentation: %+v", req.Details)
	}
}
