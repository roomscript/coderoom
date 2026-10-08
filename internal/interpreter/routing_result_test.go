package interpreter

import (
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/roomscript/coderoom/internal/agent"
	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/room"
	"github.com/roomscript/coderoom/internal/session"
)

func TestInstructionRunner_usesReportedDeliveriesForFooters(t *testing.T) {
	tests := []struct {
		name       string
		status     session.DeliveryStatus
		commandErr error
		wantRecord bool
	}{
		{"partial", session.DeliveryFailed, errors.New("partial failure"), true},
		{"not attempted", session.DeliveryNotAttempted, errors.New("primary rejected"), true},
		// A nil return is not evidence of delivery: the session outcome is authoritative.
		{"no accepted recipients", session.DeliveryFailed, nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			interp, sess, _ := newSubmitContractInterpreter(t)
			sess.readinessRequirements = []participant.Participant{
				{View: participant.View{Alias: "ada", Status: participant.StatusIdle, StartupReady: true}},
				{View: participant.View{Alias: "turing", Status: participant.StatusIdle, StartupReady: true}},
			}
			sess.executeErr = tt.commandErr
			sess.routingOverride = true
			sess.execute = func(_ session.Command, observer session.Observer) {
				recipients := []session.RecipientResult{{Alias: "ada", Role: session.RecipientBroadcast, Status: session.DeliveryDelivered}, {Alias: "turing", Role: session.RecipientBroadcast, Status: tt.status}}
				if !tt.wantRecord {
					recipients[0].Status = session.DeliveryFailed
				}
				observer.OnEvent(session.RoutingCompleted{Result: session.RoutingResult{Kind: session.RoutingBroadcast, Recipients: recipients, Err: tt.commandErr}})
			}
			mustSubmit(t, interp.Submit("work"))
			receiveSubmitCommand(t, sess.executed)
			// Synchronize via Snapshot; the operation loop finishes the completion first.
			snapshot := interp.Snapshot()
			inputs := routingInputRecords(snapshot.Room.Records)
			if !tt.wantRecord {
				if len(inputs) != 0 {
					t.Fatalf("input records = %#v", inputs)
				}
				return
			}
			if len(inputs) != 1 || !slices.Equal(inputs[0].Routing, []string{"ada"}) {
				t.Fatalf("input records = %#v", inputs)
			}
			assertRoutingFooters(t, inputs[0], tt.status)
			assertDetachedRoutingFooters(t, interp, inputs[0])
		})
	}
}

func routingInputRecords(records []room.Record) []room.Record {
	var inputs []room.Record
	for _, record := range records {
		if record.Kind == room.KindUserInput {
			inputs = append(inputs, record)
		}
	}
	return inputs
}

func assertRoutingFooters(t *testing.T, record room.Record, status session.DeliveryStatus) {
	t.Helper()
	if status == session.DeliveryFailed && !slices.Equal(record.FailedRouting, []string{"turing"}) {
		t.Fatalf("failure footer = %#v", record)
	}
	if status == session.DeliveryNotAttempted && !slices.Equal(record.UnsentRouting, []string{"turing"}) {
		t.Fatalf("unsent footer = %#v", record)
	}
}

func assertDetachedRoutingFooters(t *testing.T, interp *Interpreter, record room.Record) {
	t.Helper()
	wantRouting := slices.Clone(record.Routing)
	wantFailed := slices.Clone(record.FailedRouting)
	wantUnsent := slices.Clone(record.UnsentRouting)
	for _, field := range [][]string{record.Routing, record.FailedRouting, record.UnsentRouting} {
		for index := range field {
			field[index] = "mutated"
		}
	}
	next := interp.Snapshot().Room.Records[0]
	if !slices.Equal(next.Routing, wantRouting) || !slices.Equal(next.FailedRouting, wantFailed) || !slices.Equal(next.UnsentRouting, wantUnsent) {
		t.Fatalf("projection mutated: %#v", next)
	}
}

func TestInstructionRunner_missingRoutingOutcomeIsContractFailure(t *testing.T) {
	original := errors.New("execution failed")
	for _, commandErr := range []error{nil, original} {
		t.Run(fmt.Sprint(commandErr), func(t *testing.T) {
			interp, sess, events := newSubmitContractInterpreter(t)
			sess.readinessRequirements = []participant.Participant{{View: participant.View{Alias: "ada", Status: participant.StatusIdle, StartupReady: true}}}
			sess.routingOverride = true
			sess.executeErr = commandErr
			mustSubmit(t, interp.Submit("work"))
			receiveSubmitEvent[InputAccepted](t, events)
			failed := receiveSubmitEvent[SubmissionFailed](t, events)
			if !errors.Is(failed.Err, errMissingRoutingOutcome) {
				t.Fatalf("failure = %v", failed.Err)
			}
			if commandErr != nil && !errors.Is(failed.Err, original) {
				t.Fatalf("original error lost: %v", failed.Err)
			}
			if records := routingInputRecords(interp.Snapshot().Room.Records); len(records) != 0 {
				t.Fatalf("input records = %#v", records)
			}
		})
	}
}

func TestInstructionRunner_nonRoutingRequestDoesNotRequireOutcome(t *testing.T) {
	accepted, err := routingAcceptance(cancelRequest{alias: "ada"}, session.RoutingResult{}, nil)
	if !accepted || err != nil {
		t.Fatalf("acceptance = %v, error = %v", accepted, err)
	}
}

func TestInstructionRunner_partialDeliveryRecordsInputBeforeCausalOutput(t *testing.T) {
	interp, sess, _ := newSubmitContractInterpreter(t)
	sess.readinessRequirements = []participant.Participant{
		{View: participant.View{Alias: "ada", Status: participant.StatusIdle, StartupReady: true}},
		{View: participant.View{Alias: "turing", Status: participant.StatusIdle, StartupReady: true}},
	}
	failure := errors.New("turing rejected delivery")
	sess.executeErr = failure
	sess.routingOverride = true
	sess.execute = func(_ session.Command, observer session.Observer) {
		// Output arrives during Execute, before the routing outcome and return error.
		observer.OnEvent(session.AgentMessage{Alias: "ada", Msg: agent.Message{Mode: agent.ModeSingle, Content: agent.Output{Text: "response"}}})
		observer.OnEvent(session.RoutingCompleted{Result: session.RoutingResult{
			Kind: session.RoutingBroadcast, Err: failure,
			Recipients: []session.RecipientResult{
				{Alias: "ada", Role: session.RecipientBroadcast, Status: session.DeliveryDelivered},
				{Alias: "turing", Role: session.RecipientBroadcast, Status: session.DeliveryFailed, Err: failure},
			},
		}})
	}
	mustSubmit(t, interp.Submit("work"))
	records := interp.Snapshot().Room.Records
	if len(records) != 2 || records[0].Kind != room.KindUserInput || records[0].Text != "work" || records[1].Kind != room.KindAgentOutput {
		t.Fatalf("record order = %#v", records)
	}
	if len(routingInputRecords(records)) != 1 {
		t.Fatalf("duplicate input record: %#v", records)
	}
	if records[1].Msg == nil || records[1].Msg.Content.(agent.Output).Text != "response" {
		t.Fatalf("output = %#v", records[1])
	}
	if !slices.Equal(records[0].Routing, []string{"ada"}) || !slices.Equal(records[0].FailedRouting, []string{"turing"}) {
		t.Fatalf("routing = %#v", records[0])
	}
}
