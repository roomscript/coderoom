package std_test

import (
	"testing"

	"github.com/roomscript/coderoom/internal/interpreter/runtime"
	"github.com/roomscript/coderoom/internal/interpreter/std"
	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/session"
)

func TestParticipantCommands_captureIdentityBeforeExecution(t *testing.T) {
	alias := newTestAlias("ada")
	for _, tt := range []struct {
		raw     string
		command runtime.Command
		want    session.Command
	}{
		{"/invite ada", std.InviteCommand{}, session.InviteCommand{Alias: "ada"}},
		{"/remove ada", std.RemoveCommand{}, session.RemoveCommand{Alias: alias}},
		{"/cancel ada", std.CancelCommand{}, session.CancelCommand{Alias: alias}},
		{"/remove missing", std.RemoveCommand{}, session.RemoveCommand{Alias: participant.Alias{Name: "missing"}}},
		{"/cancel missing", std.CancelCommand{}, session.CancelCommand{Alias: participant.Alias{Name: "missing"}}},
	} {
		t.Run(tt.raw, func(t *testing.T) {
			host := &loopHost{sendDelivery: &sendDelivery{views: []participant.View{{Alias: alias}}}}
			invocation, err := tt.command.Prepare(parseSendStatement(t, tt.raw), runtime.Context{Session: host})
			if err != nil {
				t.Fatal(err)
			}
			defer invocation.Close()
			if len(host.scopes) != 0 || invocation.Staging() != nil {
				t.Fatal("preparation launched or staged participant work")
			}
			host.views[0].Alias = newTestAlias("ada") // A replacement cannot retarget the prepared command.
			var results []runtime.CommandResult
			if err := invocation.Go(func(result runtime.CommandResult) { results = append(results, result) }); err != nil {
				t.Fatal(err)
			}
			scope := host.scopes[0]
			if scope.command != tt.want || len(results) != 0 {
				t.Fatalf("command=%#v results=%#v", scope.command, results)
			}
			scope.handle(runtime.ExecutionCompleted{ExecutionID: 41})
			assertSessionCommandResult(t, results, false)
			invocation.Close()
			if !scope.closed {
				t.Fatal("execution subscription retained")
			}
		})
	}
}

func TestParticipantCommands_requireStatementAndExecution(t *testing.T) {
	for _, command := range []runtime.Command{std.InviteCommand{}, std.RemoveCommand{}, std.CancelCommand{}} {
		for _, raw := range []string{"/who", "/" + command.Name() + " ada"} {
			invocation, err := command.Prepare(parseSendStatement(t, raw), runtime.Context{})
			if err == nil || invocation != nil {
				t.Fatalf("command=%s invocation=%#v error=%v", command.Name(), invocation, err)
			}
		}
	}
}
