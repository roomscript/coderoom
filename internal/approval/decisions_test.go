package approval

import (
	"context"
	"testing"

	"github.com/roomscript/coderoom/internal/agent"
)

func TestOperationDecisions(t *testing.T) {
	for _, tt := range []struct {
		name              string
		operation         agent.Operation
		gitleaksInstalled bool
		want              Result
	}{
		{"absent", nil, true, review("policy.evidence_incomplete")},
		{"unsupported", (*agent.CommandOperation)(nil), true, review("policy.evidence_incomplete")},
		{"shell read", agent.CommandOperation{Command: "ls"}, true, review("shell.cwd_unvalidated")},
		{"shell pipeline", agent.CommandOperation{Command: "cat file | head"}, true, review("shell.cwd_unvalidated")},
		{"shell GET installed scanner", agent.CommandOperation{Command: "curl https://example.com"}, true, review("shell.cwd_unvalidated")},
		{"shell GET missing scanner", agent.CommandOperation{Command: "curl https://example.com"}, false, review("shell.cwd_unvalidated")},
		{"partial files", agent.FileOperation{Paths: []string{"file"}}, true, review("policy.evidence_incomplete")},
		{"empty file targets", agent.FileOperation{AllTargetsKnown: true}, true, review("policy.evidence_incomplete")},
		{"GET missing scanner", agent.HTTPOperation{Method: "GET", URL: "https://example.com"}, false, review("get.scanner_missing")},
		{"GET installed scanner", agent.HTTPOperation{Method: "GET", URL: "https://example.com"}, true, Result{Outcome: Decline, Reason: "get.scanner_error"}},
		{"other HTTP method", agent.HTTPOperation{Method: "POST"}, true, review("get.method_unsupported")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("PATH", t.TempDir())
			p := Policy{Scanner: &fakeScanner{installed: tt.gitleaksInstalled, result: decline("get.scanner_error")}}
			if got := p.Evaluate(context.Background(), tt.operation); got != tt.want {
				t.Fatalf("result = %+v; want %+v", got, tt.want)
			}
		})
	}
}

func TestCanceledEvaluationDoesNotCheckDependencies(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	t.Setenv("PATH", t.TempDir())
	p := Policy{Scanner: canceledScanner{t: t}}
	got := p.Evaluate(ctx, agent.HTTPOperation{Method: "GET"})
	if got != review("policy.canceled") {
		t.Fatalf("result = %+v", got)
	}
}

func TestGitleaksDiscoveryUsesLocalPath(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	got := (Policy{}).Evaluate(context.Background(), agent.HTTPOperation{Method: "GET"})
	if got != review("get.scanner_missing") {
		t.Fatalf("result = %+v", got)
	}
}

type canceledScanner struct{ t *testing.T }

func (s canceledScanner) Discover() bool {
	s.t.Fatal("canceled evaluation checked installation")
	return false
}
func (s canceledScanner) Scan(context.Context, []byte) Result {
	s.t.Fatal("canceled evaluation scanned")
	return Result{}
}
