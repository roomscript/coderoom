package session

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/roomscript/coderoom/internal/agent"
	"github.com/roomscript/coderoom/internal/approval"
	"github.com/roomscript/coderoom/internal/participant"
)

type approvalAuditObserver func(Event)

func (f approvalAuditObserver) OnEvent(e Event) { f(e) }

func TestProjectOperationPreservesInput(t *testing.T) {
	for _, tt := range []struct {
		name            string
		operation, want agent.Operation
	}{
		{"file default cwd", agent.FileOperation{Paths: []string{"/project/file"}, AllTargetsKnown: true}, agent.FileOperation{Cwd: "/project", Paths: []string{"/project/file"}, AllTargetsKnown: true}},
		{"file explicit cwd", agent.FileOperation{Cwd: "/explicit", Paths: []string{"file"}}, agent.FileOperation{Cwd: "/explicit", Paths: []string{"file"}}},
		{"command unknown cwd", agent.CommandOperation{Command: "ls"}, agent.CommandOperation{Command: "ls"}},
		{"partial HTTP", agent.HTTPOperation{Method: "GET"}, agent.HTTPOperation{Method: "GET"}},
		{"absent", nil, nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			before, err := json.Marshal(tt.operation)
			if err != nil {
				t.Fatal(err)
			}
			if got := projectOperation(tt.operation, "/project"); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("operation = %+v; want %+v", got, tt.want)
			}
			after, err := json.Marshal(tt.operation)
			if err != nil {
				t.Fatal(err)
			}
			if string(before) != string(after) {
				t.Fatal("caller operation mutated")
			}
		})
	}
}

func TestApprovalPolicyIndependentOfRole(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "read.md")
	outside := filepath.Join(t.TempDir(), "outside.md")
	if err := os.WriteFile(file, nil, 0600); err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{"builder", "reviewer", "architect", "documenter", "tester", ""} {
		t.Run(role, func(t *testing.T) {
			var s *Session
			s = New(WithApprovalPolicy(root), WithObserver(approvalAuditObserver(func(event Event) {
				switch event := event.(type) {
				case AgentLog:
					if strings.Contains(event.Text, "PRIVATE") {
						t.Error("secret in policy audit")
					}
				case ApprovalRequested:
					s.approvals.resolve(event.ID, agent.OptionDecline)
				}
			})))
			defer s.Shutdown()
			person := participant.New("ada", role, participant.InitiativeManual)
			if err := s.addParticipant(person); err != nil {
				t.Fatal(err)
			}
			s.approvalPolicy.Ignored = func(_ context.Context, path string) (bool, error) { return filepath.Base(path) == "ignored.md", nil }
			for _, tt := range []struct {
				name, path string
				write      bool
				want       agent.ApprovalOption
			}{
				{"read", file, false, agent.OptionAccept},
				{"write", filepath.Join(root, "new.md"), true, agent.OptionAccept},
				{"protected", filepath.Join(root, "AGENTS.md"), true, agent.OptionDecline},
				{"ignored", filepath.Join(root, "ignored.md"), true, agent.OptionDecline},
				{"outside", outside, true, agent.OptionDecline},
			} {
				t.Run(tt.name, func(t *testing.T) {
					ctx, cancel := context.WithTimeout(context.Background(), time.Second)
					defer cancel()
					req := agent.ApprovalRequest{Options: []agent.ApprovalOption{agent.OptionAccept, agent.OptionDecline}, Details: agent.ApprovalDetails{RequestID: "PRIVATE", Raw: json.RawMessage(`{"content":"PRIVATE"}`), Operation: agent.FileOperation{Paths: []string{tt.path}, Write: tt.write, AllTargetsKnown: true}}}
					got, err := s.ApprovalListener(person.Alias).Decide(ctx, req)
					if err != nil || got.Choice != tt.want {
						t.Fatalf("got %+v, %v; want %s", got, err, tt.want)
					}
				})
			}
		})
	}
}

func TestApprovalPolicyCancellationDuringAudit(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "read.md")
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := New(WithApprovalPolicy(root), WithObserver(approvalAuditObserver(func(Event) { cancel() })))
	defer s.Shutdown()
	req := agent.ApprovalRequest{Options: []agent.ApprovalOption{agent.OptionAccept}, Details: agent.ApprovalDetails{Operation: agent.FileOperation{Paths: []string{path}, AllTargetsKnown: true}}}
	decision, err := s.decideApproval(ctx, participant.Alias{}, req, nil)
	if err == nil || decision.Choice != "" {
		t.Fatal("canceled evaluation returned a stale grant")
	}
}

func TestApprovalPolicyShutdownStopsFileCheck(t *testing.T) {
	root := t.TempDir()
	s := New(WithApprovalPolicy(root))
	defer s.Shutdown()
	started := make(chan struct{})
	s.approvalPolicy.Ignored = func(ctx context.Context, _ string) (bool, error) {
		close(started)
		<-ctx.Done()
		return false, ctx.Err()
	}
	done := make(chan error, 1)
	go func() {
		_, err := s.decideApproval(context.Background(), participant.Alias{}, agent.ApprovalRequest{Details: agent.ApprovalDetails{Operation: agent.FileOperation{Paths: []string{filepath.Join(root, "new.md")}, Write: true, AllTargetsKnown: true}}}, nil)
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("file check did not start")
	}
	s.Shutdown()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("shutdown returned a grant")
		}
	case <-time.After(time.Second):
		t.Fatal("file check did not stop")
	}
}

func TestPolicyApprovalChoice(t *testing.T) {
	for _, tt := range []struct {
		outcome   approval.Outcome
		choice    agent.ApprovalOption
		automatic bool
	}{
		{approval.Approve, agent.OptionAccept, true},
		{approval.Decline, agent.OptionDecline, true},
		{approval.Review, "", false},
		{"", "", false},
	} {
		t.Run(string(tt.outcome), func(t *testing.T) {
			choice, automatic := policyApprovalChoice(tt.outcome)
			if choice != tt.choice || automatic != tt.automatic {
				t.Fatalf("response = %q, %v", choice, automatic)
			}
		})
	}
}

func TestDefaultApprovalPolicyShell(t *testing.T) {
	root := t.TempDir()
	isolateGitTestEnvironment(t)
	t.Setenv("HOME", root)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, ".config"))
	t.Setenv("PATH", "/usr/bin:/bin")
	if err := os.Mkdir(filepath.Join(root, "docs"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "design.md"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	s := New(WithApprovalPolicy(root))
	defer s.Shutdown()
	alias := registerIdleBeforeReadyParticipant(t, s)
	for _, command := range []string{"ls -la docs", "cat docs/design.md | head -n 20", "git status --short; git diff --stat; echo done", "cd docs && ls && head -n 20 design.md", "/bin/sh -c 'git status --short && git diff --stat'", "echo '$(rm -rf .)'"} {
		t.Run(command, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			req := agent.ApprovalRequest{Options: []agent.ApprovalOption{agent.OptionAccept}, Details: agent.ApprovalDetails{Operation: agent.CommandOperation{Command: command, Cwd: root}}}
			got, err := s.ApprovalListener(alias).Decide(ctx, req)
			if err != nil || got.Choice != agent.OptionAccept {
				t.Fatalf("got %+v, %v", got, err)
			}
		})
	}
}

type waitingGETScanner struct{ started chan struct{} }

func (*waitingGETScanner) Discover() bool { return true }
func (s *waitingGETScanner) Scan(ctx context.Context, _ []byte) approval.Result {
	close(s.started)
	<-ctx.Done()
	return approval.Result{Outcome: approval.Approve, Reason: "get.scan_clean"}
}

func TestApprovalPolicyShutdownStopsGETScan(t *testing.T) {
	s := New(WithApprovalPolicy(t.TempDir()))
	defer s.Shutdown()
	scanner := &waitingGETScanner{started: make(chan struct{})}
	s.approvalPolicy.Scanner = scanner
	done := make(chan error, 1)
	go func() {
		_, err := s.decideApproval(context.Background(), participant.Alias{}, agent.ApprovalRequest{Details: agent.ApprovalDetails{Operation: agent.HTTPOperation{Method: "GET", URL: "https://example.com"}}}, nil)
		done <- err
	}()
	select {
	case <-scanner.started:
	case <-time.After(time.Second):
		t.Fatal("scanner did not start")
	}
	s.Shutdown()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("shutdown returned a grant")
		}
	case <-time.After(time.Second):
		t.Fatal("scan not canceled")
	}
}

// Git approval fixtures must not inherit the caller's pager or Git overrides.
func isolateGitTestEnvironment(t *testing.T) {
	t.Helper()
	t.Setenv("PAGER", "cat")
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if !strings.HasPrefix(key, "GIT_") {
			continue
		}
		// Setenv registers restoration; an empty value still counts as an override
		// to the policy, so remove the variable entirely for the fixture.
		t.Setenv(key, "")
		if err := os.Unsetenv(key); err != nil {
			t.Fatal(err)
		}
	}
}
