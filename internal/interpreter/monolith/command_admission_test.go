package monolith

import (
	"testing"

	"github.com/roomscript/coderoom/internal/interpreter/runtime"
	"github.com/roomscript/coderoom/internal/room"
	"github.com/roomscript/coderoom/internal/session"
)

func TestCommandSubmission_nativeCausalPublication(t *testing.T) {
	for _, scenario := range []string{"direct", "partial", "rejected", "missing outcome"} {
		t.Run(scenario, func(t *testing.T) {
			interp, sess, events := newSubmitContractInterpreter(t)
			configureNativeSendDelivery(t, sess, scenario)
			statement := parseSourceStatement(t, "@ada work")
			mustSubmit(t, interp.Schedule(func() {
				submission := interp.AdmitCommand("send", statement)
				if submission == nil {
					t.Error("command admission rejected")
					return
				}
				if submission.Context.Participants == nil || submission.Context.Shell == nil {
					t.Error("missing command capabilities")
				}
				execution := submission.Context.Session
				unsubscribe := execution.Subscribe(func(event session.Event) {
					routed, ok := event.(session.RoutingCompleted)
					if !ok {
						return
					}
					result := deriveAdmissionTestCompletion(statement.Raw, routed.Result)
					submission.QueueCompletion(func() { submission.PublishCompletion(result) })
				})
				command := session.SendToParticipantCommand{Plan: execution.CreateParticipantSendPlan(testAlias("ada")), Message: "work", Notice: "@ada: work"}
				err := execution.Execute(command)
				if err != nil {
					unsubscribe()
				}
				submission.PublishSubmission(err)
			}))
			receiveSubmitEvent[InputAccepted](t, events)
			receiveSubmitEvent[SubmissionSucceeded](t, events)
			receiveSubmitCommand(t, sess.executed)
			if scenario != "direct" {
				receiveSubmitEvent[OperationFailed](t, events)
			}
			receiveSubmitEvent[StateChanged](t, events)
			interp.Close()
			assertNativeSendRecords(t, interp.Snapshot().Room.Records, scenario)
			if len(interp.executor.commandSessions) != 0 || len(interp.executor.pendingInvocations) != 0 {
				t.Fatal("capability path registered monolith-owned invocation state")
			}
		})
	}
}

func deriveAdmissionTestCompletion(raw string, result session.RoutingResult) runtime.CommandResult {
	completion := runtime.CommandResult{Err: result.Err}
	delivered := result.Aliases(session.DeliveryDelivered)
	if len(delivered) != 0 {
		completion.Records = []room.Record{{Kind: room.KindUserInput, Text: raw, Routing: delivered, FailedRouting: result.Aliases(session.DeliveryFailed)}}
	}
	return completion
}
