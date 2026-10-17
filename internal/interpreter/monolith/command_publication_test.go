package monolith

import (
	"testing"

	"github.com/roomscript/coderoom/internal/interpreter/runtime"
	"github.com/roomscript/coderoom/internal/room"
	"github.com/roomscript/coderoom/internal/session"
)

func TestCommandSubmission_updatesAndFinalPreserveCausalOrder(t *testing.T) {
	interp, sess, _ := newSubmitContractInterpreter(t)
	configureNativeSendDelivery(t, sess, "direct")
	statement := parseSourceStatement(t, "@ada work")
	mustSubmit(t, interp.Schedule(func() {
		submission := interp.AdmitCommand("send", statement)
		provider := submission.Context.Session
		execution := provider.CreateExecution()
		execution.Subscribe(func(event runtime.ExecutionEvent) {
			result := deriveAdmissionEventCompletion(statement.Raw, event)
			submission.QueueCompletion(func() {
				// All three outputs share one delivery burst. Updates follow causal output;
				// final delivery's input record still precedes that output.
				submission.PublishRecords([]room.Record{{Kind: room.KindSystem, Text: "first update"}}, LoopStatus{Message: "first update"})
				submission.PublishRecords([]room.Record{{Kind: room.KindSystem, Text: "second update"}}, LoopStatus{Message: "second update"})
				submission.PublishCompletion(result)
			})
		})
		command := session.SendToParticipantCommand{Plan: provider.CreateParticipantSendPlan(testAlias("ada")), Message: "work", Notice: "@ada: work"}
		if err := execution.Execute(command); err != nil {
			t.Error(err)
		}
	}))
	interp.Snapshot()
	records := interp.Snapshot().Room.Records
	if len(records) != 4 {
		t.Fatalf("records=%#v", records)
	}
	if records[0].Kind != room.KindUserInput || records[0].Text != statement.Raw || records[1].Text != "response" {
		t.Fatalf("causal record order=%#v", records)
	}
	if records[2].Text != "first update" || records[3].Text != "second update" {
		t.Fatalf("updates overwritten or reordered: %#v", records)
	}
}

func TestCommandSubmission_explicitEventsKeepSourceMetadata(t *testing.T) {
	interp, _, events := newSubmitContractInterpreter(t)
	statement := parseSourceStatement(t, "/who")
	mustSubmit(t, interp.Schedule(func() {
		submission := interp.AdmitCommand("who", statement)
		submission.PublishRecords(nil, ShellCompleted{Command: "/condition"})
		execution := submission.Context.Session.CreateExecution()
		execution.Subscribe(func(runtime.ExecutionEvent) {})()
		submission.Close()
	}))
	completed := receiveSubmitEvent[ShellCompleted](t, events)
	if completed.Statement.Raw != statement.Raw {
		t.Fatalf("statement=%#v", completed.Statement)
	}
}
