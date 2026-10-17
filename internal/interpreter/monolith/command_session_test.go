package monolith

import (
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/roomscript/coderoom/internal/agent"
	"github.com/roomscript/coderoom/internal/interpreter/runtime"
	"github.com/roomscript/coderoom/internal/interpreter/std"
	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/promptlang"
	"github.com/roomscript/coderoom/internal/room"
	"github.com/roomscript/coderoom/internal/session"
)

func TestNativeSendBridge_delivery(t *testing.T) {
	for _, scenario := range []string{"direct", "partial", "rejected", "missing outcome"} {
		t.Run(scenario, func(t *testing.T) {
			interp, sess, events := newSubmitContractInterpreter(t)
			configureNativeSendDelivery(t, sess, scenario)
			mustSubmit(t, interp.SubmitCommand(std.SendCommand{}, parseSourceStatement(t, "@ada work")))
			receiveSubmitEvent[InputAccepted](t, events)
			receiveSubmitEvent[SubmissionSucceeded](t, events)
			receiveSubmitCommand(t, sess.executed)
			if scenario != "direct" {
				failed := receiveSubmitEvent[OperationFailed](t, events)
				if failed.Raw != "@ada work" || failed.Operation != "send" || failed.Err == nil {
					t.Fatalf("failure = %#v", failed)
				}
			}
			receiveSubmitEvent[StateChanged](t, events)
			// Native dispatch follows the submission operation; synchronize twice.
			interp.Snapshot()
			records := interp.Snapshot().Room.Records
			assertNativeSendRecords(t, records, scenario)
			if len(interp.executor.commandSessions) != 0 {
				t.Fatal("retained completed invocation")
			}
		})
	}
}

// Queue both submissions before either dispatch. The second primary reservation
// fails at launch; a legacy result to the same primary cannot complete the first.
func TestNativeSendBridge_samePrimaryAndLegacyOwnership(t *testing.T) {
	interp, sess, _ := newSubmitContractInterpreter(t)
	sess.mu.Lock()
	sess.participants = []participant.View{{Alias: testAlias("ada"), Status: participant.StatusIdle, StartupReady: true}}
	sess.mu.Unlock()
	entered, release := make(chan struct{}), make(chan struct{})
	blocker := &callbackTestCommand{callbacks: make(chan func(runtime.CommandResult), 1), entered: entered, release: release}
	enqueueCallbackTest(t, interp, blocker)
	<-entered
	mustSubmit(t, interp.SubmitCommand(std.SendCommand{}, parseSourceStatement(t, "@ada first")))
	mustSubmit(t, interp.Submit("@ada legacy"))
	mustSubmit(t, interp.SubmitCommand(std.SendCommand{}, parseSourceStatement(t, "@ada second")))
	close(release)
	receiveSubmitCommand(t, sess.executed)
	receiveSubmitCommand(t, sess.executed)
	interp.Snapshot()
	records := interp.Snapshot().Room.Records
	var routed []string
	for _, record := range records {
		if len(record.Routing) != 0 {
			routed = append(routed, record.Text)
		}
	}
	if !slices.Equal(routed, []string{"@ada legacy", "@ada first"}) {
		t.Fatalf("routed inputs = %v", routed)
	}
	if sess.maxActive.Load() != 1 {
		t.Fatal("concurrent native execution")
	}
}

func TestNativeSendBridge_shutdownDrainsAdmittedDelivery(t *testing.T) {
	interp, sess, events := newSubmitContractInterpreter(t)
	entered, release := make(chan struct{}), make(chan struct{})
	sess.execute = func(session.Command, session.Observer) { close(entered); <-release }
	mustSubmit(t, interp.SubmitCommand(std.SendCommand{}, parseSourceStatement(t, "@ada work")))
	receiveSubmitEvent[InputAccepted](t, events)
	receiveSubmitEvent[SubmissionSucceeded](t, events)
	<-entered
	interp.executor.requestClose()
	close(release)
	interp.Close()
	records := interp.Snapshot().Room.Records
	if len(records) != 1 || records[0].Text != "@ada work" || !slices.Equal(records[0].Routing, []string{"ada"}) {
		t.Fatalf("shutdown lost admitted delivery: %#v", records)
	}
	if len(interp.executor.commandSessions) != 0 || len(interp.executor.legacySends) != 0 {
		t.Fatal("shutdown retained send ownership or subscriptions")
	}
}

func configureNativeSendDelivery(t *testing.T, sess *submitContractSession, scenario string) {
	t.Helper()
	failure := errors.New("delivery failed")
	sess.routingOverride = true
	if scenario == "partial" || scenario == "rejected" {
		sess.executeErr = failure
	}
	sess.execute = func(command session.Command, observer session.Observer) {
		send := command.(session.SendToParticipantCommand)
		if send.Message != "work" || send.Notice != "@ada: work" {
			t.Errorf("send = %#v", send)
		}
		if scenario == "missing outcome" {
			return
		}
		result := session.RoutingResult{Kind: session.RoutingParticipantSend, Recipients: []session.RecipientResult{
			{Alias: send.Plan.PrimaryParticipant(), Role: session.RecipientPrimary, Status: session.DeliveryDelivered},
		}}
		if scenario == "rejected" {
			result.Recipients[0].Status = session.DeliveryFailed
			result.Err = failure
		}
		if scenario == "partial" {
			result.Err = failure
			result.Recipients = append(result.Recipients, session.RecipientResult{Alias: testAlias("ben"), Role: session.RecipientNotice, Status: session.DeliveryFailed})
		}
		if scenario != "rejected" {
			observer.OnEvent(session.AgentMessage{Alias: testAlias("ada"), Msg: agent.Message{Mode: agent.ModeSingle, Content: agent.Output{Text: "response"}}})
		}
		observer.OnEvent(session.RoutingCompleted{Result: result})
	}
}

func assertNativeSendRecords(t *testing.T, records []room.Record, scenario string) {
	t.Helper()
	if scenario == "rejected" || scenario == "missing outcome" {
		if len(records) != 0 {
			t.Fatalf("rejected input recorded: %#v", records)
		}
	} else {
		assertNativeSendOutput(t, records, scenario)
	}
}

func assertNativeSendOutput(t *testing.T, records []room.Record, scenario string) {
	t.Helper()
	if len(records) != 2 || records[0].Kind != room.KindUserInput || records[0].Text != "@ada work" || records[1].Kind != room.KindAgentOutput {
		t.Fatalf("causal records = %#v", records)
	}
	if !slices.Equal(records[0].Routing, []string{"ada"}) {
		t.Fatalf("routing = %#v", records[0])
	}
	if scenario == "partial" && !slices.Equal(records[0].FailedRouting, []string{"ben"}) {
		t.Fatalf("partial routing = %#v", records[0])
	}
}

// The lifecycle monitor will admit from a worker, rather than from the host lane.
type workerSendCommand struct{ std.SendCommand }

func (c workerSendCommand) Prepare(parsed promptlang.ParsedStatement, ctx runtime.Context) (runtime.Invocation, error) {
	invocation, err := c.SendCommand.Prepare(parsed, ctx)
	if err != nil {
		return nil, fmt.Errorf("prepare worker send: %w", err)
	}
	return &workerSendInvocation{Invocation: invocation, done: make(chan struct{})}, nil
}

type workerSendInvocation struct {
	runtime.Invocation
	done chan struct{}
}

func (i *workerSendInvocation) Go(complete func(runtime.CommandResult)) error {
	go func() {
		defer close(i.done)
		if err := i.Invocation.Go(complete); err != nil {
			complete(runtime.CommandResult{Err: err})
		}
	}()
	return nil
}

func (i *workerSendInvocation) Close() {
	<-i.done
	i.Invocation.Close()
}

func TestNativeSendBridge_workerAdmission(t *testing.T) {
	interp, sess, events := newSubmitContractInterpreter(t)
	mustSubmit(t, interp.SubmitCommand(workerSendCommand{}, parseSourceStatement(t, "@ada work")))
	receiveSubmitEvent[InputAccepted](t, events)
	receiveSubmitEvent[SubmissionSucceeded](t, events)
	receiveSubmitCommand(t, sess.executed)
	receiveSubmitEvent[StateChanged](t, events)
	records := interp.Snapshot().Room.Records
	if len(records) != 1 || records[0].Text != "@ada work" || !slices.Equal(records[0].Routing, []string{"ada"}) {
		t.Fatalf("worker delivery = %#v", records)
	}
}
