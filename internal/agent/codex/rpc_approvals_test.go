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
	if len(req.Ask) >= len(command) {
		t.Fatal("fixture did not exercise shortened display")
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

func TestPermissionExpansionsHaveNoInferredOperation(t *testing.T) {
	for _, tt := range []struct{ method, raw string }{
		{methodFileChangeRequestApproval, `{"grantRoot":"/project","reason":"write access"}`},
		{methodPermissionsRequestApproval, `{"cwd":"/project","permissions":{"network":{}}}`},
	} {
		t.Run(tt.method, func(t *testing.T) {
			req, _, err := normalizeApproval(tt.method, json.RawMessage(tt.raw))
			if err != nil {
				t.Fatal(err)
			}
			if req.Details.Operation != nil {
				t.Fatal("permission expansion inferred a concrete operation")
			}
			if req.Details.Backend != "codex" || req.Details.Tool != tt.method || string(req.Details.Raw) != tt.raw {
				t.Fatal("permission evidence lost")
			}
		})
	}
}
