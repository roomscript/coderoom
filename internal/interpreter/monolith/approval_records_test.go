package monolith

import (
	"strings"
	"testing"

	"github.com/roomscript/coderoom/internal/agent"
	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/session"
)

func TestApprovalRecordSettlesAfterBoxDismissalBeforeSuccessor(t *testing.T) {
	m := newInterpreterModel()
	t.Cleanup(m.Close)
	alias := participant.New("bill", "builder", participant.InitiativeManual).Alias
	req := agent.ApprovalRequest{Kind: agent.ApprovalFileChange, Details: agent.ApprovalDetails{Operation: agent.FileOperation{Paths: []string{"file"}, ProposedChanges: []agent.FileChange{{Path: "file", Diff: "+evidence"}}}}, Options: []agent.ApprovalOption{agent.OptionAccept}}
	m.ApplySessionEvent(session.ApprovalRequested{ID: 1, Alias: alias, Req: req})
	detached := m.Snapshot()
	detached.approval.Paths[0] = "changed"
	if m.Snapshot().approval.Paths[0] != "file" {
		t.Fatal("approval paths are not detached")
	}
	m.ClearApproval(1) // Accepted command dismissed the box; settlement is still a Session fact.
	if !m.ApplySessionEvent(session.ApprovalCleared{ID: 1, Alias: alias, Choice: agent.OptionAccept}) {
		t.Fatal("dismissal suppressed settlement")
	}
	command := agent.CommandOperation{Command: "/opt/git diff\n" + strings.Repeat("full evidence\n", 100), Cwd: "/authoritative", Explanation: "supplied reason"}
	nextRequest := agent.ApprovalRequest{Kind: agent.ApprovalCommandExecution, Details: agent.ApprovalDetails{Operation: command}, Options: req.Options}
	m.ApplySessionEvent(session.ApprovalRequested{ID: 2, Alias: alias, Req: nextRequest})
	changes := m.TakeTranscriptChanges()
	if len(changes) != 3 {
		t.Fatalf("changes = %d", len(changes))
	}
	first := changes[0].Delta.RecordUpdates[0]
	settled := changes[1].Delta.RecordUpdates[0]
	next := changes[2].Delta.RecordUpdates[0]
	if first.Index != settled.Index || next.Index <= settled.Index || settled.Record.Decision != "Approved by you · once" || !strings.Contains(settled.Record.Text, "+evidence") {
		t.Fatal("record order, settlement or evidence lost")
	}
	snapshot := m.Snapshot()
	assertCommandProjection(t, next.Record.Text, snapshot.approval, command)
	m.ApplySessionEvent(session.ApprovalCleared{ID: 2, Alias: alias})
	changes = m.TakeTranscriptChanges()
	if len(changes) != 1 || changes[0].Delta.RecordUpdates[0].Record.Decision != "Withdrawn" {
		t.Fatal("command withdrawal lost")
	}
}

func assertCommandProjection(t *testing.T, text string, approval *Approval, command agent.CommandOperation) {
	t.Helper()
	if !strings.Contains(text, command.Command) || !strings.Contains(approval.Details, command.Explanation) || !strings.Contains(approval.Details, command.Cwd) || approval.Summary != "shell command" {
		t.Fatal("command projection lost evidence or summary")
	}
}
