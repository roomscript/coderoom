package codex

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/roomscript/coderoom/internal/agent"
	"github.com/roomscript/coderoom/internal/approval"
)

func fileEnvelope(method, thread, turn, item string) rpcEnvelope {
	raw, _ := json.Marshal(map[string]any{
		"threadId": thread, "turnId": turn, "itemId": item,
		"item": map[string]any{"type": "fileChange", "id": item, "changes": []any{
			map[string]any{"path": item + "\nfile", "diff": "@@\n-old\n+new\n", "kind": map[string]string{"type": "update"}},
		}},
	})
	id := 7
	return rpcEnvelope{Method: method, Params: raw, ID: &id}
}

func queuedFileApproval(t *testing.T, c *Client, msg rpcEnvelope) agent.ApprovalRequest {
	t.Helper()
	if !enqueueApprovalRequest(context.Background(), c, msg) {
		t.Fatal("approval not queued")
	}
	return (<-c.approvals.bufInbox).req
}

func TestFileApprovalIdentityAndCleanup(t *testing.T) {
	for _, cleanup := range []string{"none", methodItemCompleted, methodTurnCompleted, methodTurnFailed} {
		t.Run(cleanup, func(t *testing.T) {
			c := New(t.TempDir())
			c.initApprovals()
			for _, item := range []string{"a", "b"} {
				c.trackFileApprovalEvidence(fileEnvelope(methodItemStarted, "thread", "turn", item))
			}
			if cleanup != "none" {
				c.trackFileApprovalEvidence(fileEnvelope(cleanup, "thread", "turn", "a"))
			}
			for _, tt := range []struct {
				thread, turn, item string
				want               bool
			}{
				{"thread", "turn", "a", cleanup == "none"},
				{"thread", "turn", "b", cleanup == "none" || cleanup == methodItemCompleted},
				{"other", "turn", "b", false}, {"thread", "other", "b", false}, {"thread", "turn", "missing", false}, {"", "turn", "b", false},
			} {
				msg := fileEnvelope(methodFileChangeRequestApproval, tt.thread, tt.turn, tt.item)
				req := queuedFileApproval(t, c, msg)
				assertFileApprovalEvidence(t, req, msg, tt.item, tt.want)
			}
		})
	}
}

func assertFileApprovalEvidence(t *testing.T, req agent.ApprovalRequest, msg rpcEnvelope, item string, want bool) {
	t.Helper()
	operation := req.Details.Operation.(agent.FileOperation)
	changes := operation.ProposedChanges
	if (len(changes) > 0) != want {
		t.Fatalf("%s: changes = %+v, want evidence %v", msg.Params, changes, want)
	}
	if want {
		expected := []agent.FileChange{{Path: item + "\nfile", Diff: "@@\n-old\n+new\n", ChangeKind: "update"}}
		if !reflect.DeepEqual(changes, expected) {
			t.Fatalf("wrong evidence: %+v", changes)
		}
	}
	if operation.AllTargetsKnown || len(operation.Paths) != 0 || !bytes.Equal(req.Details.Raw, msg.Params) {
		t.Fatal("policy or raw evidence changed")
	}
	policy := approval.Policy{}
	if got := policy.Evaluate(context.Background(), operation); got != policy.Evaluate(context.Background(), nil) {
		t.Fatalf("display evidence changed policy result: %+v", got)
	}
}

func TestRecordedFileApprovalEvidence(t *testing.T) {
	f, err := os.Open("testdata/transcripts/0.163.0/approvals-file-change/output.transcript")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	c := New(t.TempDir())
	c.initApprovals()
	c.read.bufMessages = make(chan readMessage, 32)
	found := false
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var line struct {
			Message rpcEnvelope `json:"message"`
		}
		if json.Unmarshal(scanner.Bytes(), &line) != nil {
			continue
		}
		msg := line.Message
		applyRecordedFileEnvelope(t, c, msg)
		if msg.Method != methodFileChangeRequestApproval {
			continue
		}
		req := (<-c.approvals.bufInbox).req
		changes := req.Details.Operation.(agent.FileOperation).ProposedChanges
		if len(changes) != 1 || changes[0].Diff != "ok\n" || changes[0].ChangeKind != "add" {
			t.Fatalf("recorded evidence = %+v", changes)
		}
		found = true
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("no recorded approval")
	}
}

func applyRecordedFileEnvelope(t *testing.T, c *Client, msg rpcEnvelope) {
	t.Helper()
	if msg.Method != methodItemStarted && msg.Method != methodFileChangeRequestApproval {
		c.trackFileApprovalEvidence(msg)
		return
	}
	if !handleStdoutEnvelope(context.Background(), c, msg) {
		t.Fatal("recorded envelope rejected")
	}
}

func TestFileApprovalChoicesUnchanged(t *testing.T) {
	for _, choice := range []agent.ApprovalOption{agent.OptionAccept, agent.OptionAcceptForSession, agent.OptionDecline, agent.OptionCancel} {
		t.Run(string(choice), func(t *testing.T) {
			var out bytes.Buffer
			c := New(t.TempDir())
			c.proc.codexIn = nopWriteCloser{&out}
			if err := writeApprovalResult(c, 7, approvalContext{kind: agent.ApprovalFileChange}, choice); err != nil {
				t.Fatal(err)
			}
			var got rpcResponse[commandDecisionResult]
			if err := json.Unmarshal(out.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, rpcResponse[commandDecisionResult]{ID: 7, Result: commandDecisionResult{Decision: string(choice)}}) {
				t.Fatalf("response = %+v", got)
			}
		})
	}
}

func TestFileEvidenceReleasedOnReaderExit(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		t.Run(fmt.Sprint(canceled), func(t *testing.T) {
			c := newUnstartedWithIO(t, nopWriteCloser{&bytes.Buffer{}}, strings.NewReader(""), nil)
			c.trackFileApprovalEvidence(fileEnvelope(methodItemStarted, "thread", "turn", "a"))
			c.read.bufMessages = make(chan readMessage, 1)
			if canceled {
				c.lifecycle.cancelFn()
			}
			readCodexOutWorker(c.lifecycle.ctx, c)
			if len(c.approvals.files) != 0 {
				t.Fatal("reader exit retained evidence")
			}
		})
	}
}
