package approval

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/roomscript/coderoom/internal/agent"
)

type fakeScanner struct {
	installed bool
	result    Result
	input     []byte
	calls     int
}

func (s *fakeScanner) Discover() bool { return s.installed }
func (s *fakeScanner) Scan(_ context.Context, input []byte) Result {
	s.calls++
	s.input = input
	return s.result
}

func TestGETDecisions(t *testing.T) {
	for _, tt := range []struct {
		name      string
		installed bool
		op        agent.HTTPOperation
		scan      Result
		want      Result
		calls     int
	}{
		{"missing", false, agent.HTTPOperation{Method: "GET", URL: "https://example.com"}, Result{}, review("get.scanner_missing"), 0},
		{"clean", true, agent.HTTPOperation{Method: "GET", URL: "https://example.com"}, approve("get.scan_clean"), approve("get.scan_clean"), 1},
		{"finding", true, agent.HTTPOperation{Method: "GET", URL: "https://example.com"}, decline("get.secret_detected"), decline("get.secret_detected"), 1},
		{"redirect", true, agent.HTTPOperation{Method: "GET", URL: "https://example.com", FollowsRedirects: true}, Result{}, decline("get.validation_incomplete"), 0},
		{"dynamic", true, agent.HTTPOperation{Method: "GET", URL: "https://example.com", Dynamic: true}, Result{}, decline("get.validation_incomplete"), 0},
		{"bad URL", true, agent.HTTPOperation{Method: "GET", URL: "file:///private"}, Result{}, decline("get.validation_incomplete"), 0},
		{"method", true, agent.HTTPOperation{Method: "POST"}, Result{}, review("get.method_unsupported"), 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			scanner := &fakeScanner{installed: tt.installed, result: tt.scan}
			p := Policy{Scanner: scanner}
			if got := p.Evaluate(context.Background(), tt.op); got != tt.want || scanner.calls != tt.calls {
				t.Fatalf("got %+v calls %d", got, scanner.calls)
			}
		})
	}
}

func TestGETExplicitInputsAndWrappers(t *testing.T) {
	p := fixture(t)
	scanner := &fakeScanner{installed: true, result: approve("get.scan_clean")}
	p.Scanner = scanner
	for _, command := range []string{"curl -s -H 'Authorization: PRIVATE' 'https://example.com?q=%50%52%49%56%41%54%45'", "/bin/sh -c 'curl -X GET https://example.com'"} {
		got := p.Evaluate(context.Background(), agent.CommandOperation{Command: command, Cwd: p.ProjectRoot})
		if got.Outcome != Approve || scanner.calls == 0 {
			t.Fatalf("got %+v", got)
		}
		if strings.Contains(command, "PRIVATE") && !strings.Contains(string(scanner.input), "PRIVATE") {
			t.Fatal("outbound input missing")
		}
	}
	for _, command := range []string{"curl -L https://example.com", "curl -H @secrets https://example.com"} {
		if got := p.Evaluate(context.Background(), agent.CommandOperation{Command: command, Cwd: p.ProjectRoot}); got != decline("get.validation_incomplete") {
			t.Fatalf("got %+v", got)
		}
	}
}

func TestGETDecodingAndLimits(t *testing.T) {
	input, ok := getInput(agent.HTTPOperation{Method: "GET", URL: "https://example.com?q=UFJJVkFURQ==", Headers: map[string]string{"X-Input": "PRIVATE"}, Body: "explicit body"})
	if !ok || !strings.Contains(string(input), "PRIVATE") || !strings.Contains(string(input), "explicit body") {
		t.Fatal("missing decoded outbound input")
	}
	if _, ok := getInput(agent.HTTPOperation{Method: "GET", URL: "https://example.com/" + strings.Repeat("x", maxCommandBytes)}); ok {
		t.Fatal("oversize input accepted")
	}
}

func TestCurlConfigAndDuplicateHeaders(t *testing.T) {
	p := fixture(t)
	scanner := &fakeScanner{installed: true, result: approve("get.scan_clean")}
	p.Scanner = scanner
	if err := os.WriteFile(filepath.Join(p.ProjectRoot, ".curlrc"), []byte("location"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		command string
		outcome Outcome
	}{
		{"curl https://example.com", Decline},
		{"curl -q -H 'X-Test: PRIVATE_FIRST' -H 'X-Test: PRIVATE_SECOND' https://example.com", Approve},
		{"curl \"$URL\"", Decline},
	} {
		t.Run(tt.command, func(t *testing.T) {
			got := p.Evaluate(context.Background(), agent.CommandOperation{Command: tt.command, Cwd: p.ProjectRoot})
			if got.Outcome != tt.outcome {
				t.Fatalf("got %+v", got)
			}
			if tt.outcome == Approve && (!strings.Contains(string(scanner.input), "PRIVATE_FIRST") || !strings.Contains(string(scanner.input), "PRIVATE_SECOND")) {
				t.Fatal("lost duplicate header")
			}
		})
	}
	scanner.installed = false
	got := p.Evaluate(context.Background(), agent.CommandOperation{Command: "curl -L https://example.com", Cwd: p.ProjectRoot})
	if got != review("get.scanner_missing") {
		t.Fatalf("got %+v", got)
	}
}
