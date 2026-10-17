package std_test

import (
	"errors"
	"testing"

	"github.com/roomscript/coderoom/internal/interpreter/runtime"
	"github.com/roomscript/coderoom/internal/interpreter/std"
	"github.com/roomscript/coderoom/internal/policy"
	"github.com/roomscript/coderoom/internal/session"
)

func TestPolicyCommand_completion(t *testing.T) {
	failure := errors.New("execution failed")
	for _, tt := range []struct {
		name    string
		event   runtime.ExecutionEvent
		wantErr bool
	}{
		{"success", runtime.ExecutionCompleted{ExecutionID: 41}, false},
		{"execution error", runtime.ExecutionCompleted{ExecutionID: 42, Err: failure}, true},
		{"protocol error", runtime.ExecutionFailed{Err: failure}, true},
		{"invalid failure", runtime.ExecutionFailed{}, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			host := &loopHost{}
			invocation, err := (std.PolicyCommand{}).Prepare(parseSendStatement(t, "/policy enable send-notices"), runtime.Context{Session: host})
			if err != nil {
				t.Fatal(err)
			}
			defer invocation.Close()
			if len(host.scopes) != 0 || invocation.Staging() != nil {
				t.Fatal("preparation started work or staged policy")
			}
			if invocation.(runtime.SubmissionAcknowledgement).ReadyToAcknowledge() {
				t.Fatal("policy acknowledged before completion")
			}
			var results []runtime.CommandResult
			if err := invocation.Go(func(result runtime.CommandResult) { results = append(results, result) }); err != nil {
				t.Fatal(err)
			}
			scope := host.scopes[0]
			if scope.command != (session.EnablePolicyCommand{Name: policy.SendNotices}) || len(results) != 0 {
				t.Fatalf("command=%#v results=%#v", scope.command, results)
			}
			scope.handle(tt.event)
			assertSessionCommandResult(t, results, tt.wantErr)
			invocation.Close()
			if !scope.closed {
				t.Fatal("execution subscription retained")
			}
		})
	}
}

func TestPolicyCommand_requiresStatementAndExecution(t *testing.T) {
	for _, raw := range []string{"/who", "/policy enable echo-invites"} {
		invocation, err := (std.PolicyCommand{}).Prepare(parseSendStatement(t, raw), runtime.Context{})
		if err == nil || invocation != nil {
			t.Fatalf("invocation=%#v error=%v", invocation, err)
		}
	}
}

func assertSessionCommandResult(t *testing.T, results []runtime.CommandResult, wantErr bool) {
	t.Helper()
	if len(results) != 1 || (results[0].Err != nil) != wantErr || len(results[0].Records) != 0 {
		t.Fatalf("results=%#v", results)
	}
}
