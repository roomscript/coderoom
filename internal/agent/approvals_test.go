package agent

import (
	"strings"
	"testing"
)

func TestApprovalPrompt(t *testing.T) {
	for _, tt := range []struct {
		name string
		req  ApprovalRequest
		want string
	}{
		{"command", ApprovalRequest{Kind: ApprovalCommandExecution}, "shell command"},
		{"file", ApprovalRequest{Kind: ApprovalFileChange}, "File changes"},
		{"summary", ApprovalRequest{Kind: ApprovalPermissions, Presentation: ApprovalPresentation{Summary: "Tool: Read"}}, "Tool: Read"},
		{"reason", ApprovalRequest{Kind: ApprovalPermissions, Presentation: ApprovalPresentation{Summary: "Additional permissions", Explanation: "  full\nreason "}}, "Additional permissions —   full\nreason "},
		{"fallback", ApprovalRequest{Kind: ApprovalPermissions}, "Permission request"},
		{"unknown", ApprovalRequest{}, "Approval request"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := ApprovalPrompt(tt.req); got != tt.want {
				t.Fatalf("prompt = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestApprovalPromptRetainsActionContext(t *testing.T) {
	command := "printf '" + strings.Repeat("full command\n", 100) + "tail'"
	for _, tt := range []struct {
		name      string
		kind      ApprovalKind
		operation Operation
		evidence  string
		want      string
	}{
		{"command", ApprovalCommandExecution, CommandOperation{Command: command, Cwd: "/real/cwd"}, "", "shell command — supplied reason\nCommand: " + command + "\nWorking directory: /real/cwd"},
		{"unknown cwd", ApprovalCommandExecution, CommandOperation{Command: "make test"}, "", "shell command — supplied reason\nCommand: make test\nWorking directory: unknown"},
		{"files", ApprovalFileChange, FileOperation{Paths: []string{"/first", "/second"}, ProposedChanges: []FileChange{{Path: "/first"}, {Path: "/third"}}}, "", "File changes — supplied reason\nFiles:\n/first\n/second\n/third"},
		{"grant scope", ApprovalFileChange, FileOperation{Write: true}, "Grant root: /outside", "File changes — supplied reason\nGrant root: /outside"},
		{"unparsed input", ApprovalCommandExecution, nil, "Input (unparsed):\nbroken", "shell command — supplied reason\nInput (unparsed):\nbroken"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			req := ApprovalRequest{Kind: tt.kind, Details: ApprovalDetails{Operation: tt.operation}, Presentation: ApprovalPresentation{Explanation: "supplied reason", Evidence: tt.evidence}}
			if got := ApprovalPrompt(req); got != tt.want {
				t.Fatalf("prompt = %q, want %q", got, tt.want)
			}
		})
	}
}
