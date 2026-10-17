package monolith

import (
	"errors"
	"slices"
	"testing"

	"github.com/roomscript/coderoom/internal/agent"
	"github.com/roomscript/coderoom/internal/room"
	"github.com/roomscript/coderoom/internal/session"
)

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
