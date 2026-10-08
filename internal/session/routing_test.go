package session_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/roomscript/coderoom/internal/agent"
	"github.com/roomscript/coderoom/internal/session"
)

func TestRoutingCompleted_reportsActualOutcomes(t *testing.T) {
	failure := errors.New("adapter rejected delivery")
	tests := routingOutcomeCases(failure)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			observer := newTestObserver()
			ada, turing := newMockAgent(), newMockAgent()
			ada.sendErr, turing.sendErr = tt.primaryErr, tt.noticeErr
			s := newSession(t, session.WithObserver(observer), mappedFactory(map[string]agent.Agent{"ada": ada, "turing": turing}))
			invite(t, s, "ada")
			mustReceive[session.AgentReady](t, observer.ch)
			invite(t, s, "turing")
			mustReceive[session.AgentReady](t, observer.ch)
			enableSendNotices(t, s)
			err := s.Execute(tt.command(s))
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v", err)
			}
			outcomes, handoffs := readRoutingOutcomes(observer)
			if len(outcomes) != 1 {
				t.Fatalf("outcome count = %d", len(outcomes))
			}
			result := outcomes[0]
			if result.Kind != tt.kind || (result.Err != nil) != tt.wantErr {
				t.Fatalf("result = %#v", result)
			}
			assertRecipientOutcomes(t, result, tt.roles, tt.statuses)

			if tt.kind == session.RoutingHandoff && (handoffs == 1) != !tt.wantErr {
				t.Fatalf("handoff delivered count = %d", handoffs)
			}
			if tt.name == "primary failure skips notices" && len(turing.sends) != 0 {
				t.Fatal("notice was attempted after primary failure")
			}
		})
	}
}

func participantRoutingCommand(s *session.Session) session.Command {
	return session.SendToParticipantCommand{Plan: s.CreateParticipantSendPlan("ada"), Message: "work", Notice: "context"}
}

// A synchronous probe confirms Broadcast no longer announces delivery before
// calling the adapter, and that each observer receives detached result storage.
type routingProbe struct {
	results []session.RoutingResult
	mutate  bool
}

func (p *routingProbe) OnEvent(event session.Event) {
	if event, ok := event.(session.RoutingCompleted); ok {
		if p.mutate {
			event.Result.Recipients[0].Alias = "mutated"
		}
		p.results = append(p.results, event.Result)
	}
}

func TestRoutingCompleted_followsAdapterAcceptanceAndDetachesObservers(t *testing.T) {
	first, second := &routingProbe{mutate: true}, &routingProbe{}
	adapter := newMockAgent()
	adapter.sendHook = func(string) error {
		if len(first.results) != 0 || len(second.results) != 0 {
			t.Fatal("routing outcome preceded delivery")
		}
		return nil
	}
	ready := newTestObserver()
	s := newSession(t, session.WithObserver(first), session.WithObserver(second), session.WithObserver(ready), mappedFactory(map[string]agent.Agent{"ada": adapter}))
	invite(t, s, "ada")
	mustReceive[session.AgentReady](t, ready.ch)
	if err := s.Execute(session.BroadcastCommand{Aliases: []string{"ada"}, Text: "work"}); err != nil {
		t.Fatal(err)
	}
	if len(second.results) != 1 || second.results[0].Recipients[0].Alias != "ada" {
		t.Fatalf("second observer = %#v", second.results)
	}
}

func readRoutingOutcomes(observer *testObserver) ([]session.RoutingResult, int) {
	observer.mu.Lock()
	events := slices.Clone(observer.events)
	observer.mu.Unlock()
	var outcomes []session.RoutingResult
	handoffs := 0
	for _, event := range events {
		if event, ok := event.(session.RoutingCompleted); ok {
			outcomes = append(outcomes, event.Result)
		}
		if _, ok := event.(session.HandoffDelivered); ok {
			handoffs++
		}
	}
	return outcomes, handoffs
}

func assertRecipientOutcomes(t *testing.T, result session.RoutingResult, roles []session.RecipientRole, statuses []session.DeliveryStatus) {
	t.Helper()
	if len(result.Recipients) != len(statuses) {
		t.Fatalf("recipients = %#v", result.Recipients)
	}
	for index, recipient := range result.Recipients {
		if recipient.Role != roles[index] || recipient.Status != statuses[index] {
			t.Fatalf("recipient %d = %#v", index, recipient)
		}
		if (recipient.Err != nil) != (recipient.Status == session.DeliveryFailed) {
			t.Fatalf("recipient error = %#v", recipient)
		}
	}
}

type routingOutcomeCase struct {
	name       string
	kind       session.RoutingKind
	primaryErr error
	noticeErr  error
	command    func(*session.Session) session.Command
	roles      []session.RecipientRole
	statuses   []session.DeliveryStatus
	wantErr    bool
}

func routingOutcomeCases(failure error) []routingOutcomeCase {
	return []routingOutcomeCase{
		{"participant success", session.RoutingParticipantSend, nil, nil,
			participantRoutingCommand, []session.RecipientRole{session.RecipientPrimary, session.RecipientNotice},
			[]session.DeliveryStatus{session.DeliveryDelivered, session.DeliveryDelivered}, false},
		{"primary failure skips notices", session.RoutingParticipantSend, failure, nil,
			participantRoutingCommand, []session.RecipientRole{session.RecipientPrimary, session.RecipientNotice},
			[]session.DeliveryStatus{session.DeliveryFailed, session.DeliveryNotAttempted}, true},
		{"notice failure preserves primary delivery", session.RoutingParticipantSend, nil, failure,
			participantRoutingCommand, []session.RecipientRole{session.RecipientPrimary, session.RecipientNotice},
			[]session.DeliveryStatus{session.DeliveryDelivered, session.DeliveryFailed}, true},
		{"broadcast partial", session.RoutingBroadcast, nil, failure,
			broadcastRoutingCommand,
			[]session.RecipientRole{session.RecipientBroadcast, session.RecipientBroadcast},
			[]session.DeliveryStatus{session.DeliveryDelivered, session.DeliveryFailed}, true},
		{"broadcast total failure", session.RoutingBroadcast, failure, failure,
			broadcastRoutingCommand,
			[]session.RecipientRole{session.RecipientBroadcast, session.RecipientBroadcast},
			[]session.DeliveryStatus{session.DeliveryFailed, session.DeliveryFailed}, true},
		{"outside room", session.RoutingOutsideRoomSend, nil, nil,
			outsideRoomRoutingCommand,
			[]session.RecipientRole{session.RecipientPrimary}, []session.DeliveryStatus{session.DeliveryDelivered}, false},
		{"handoff success", session.RoutingHandoff, nil, nil,
			handoffRoutingCommand,
			[]session.RecipientRole{session.RecipientHandoff}, []session.DeliveryStatus{session.DeliveryDelivered}, false},
		{"handoff adapter failure", session.RoutingHandoff, nil, failure,
			handoffRoutingCommand,
			[]session.RecipientRole{session.RecipientHandoff}, []session.DeliveryStatus{session.DeliveryFailed}, true},
		{"handoff source rejection", session.RoutingHandoff, nil, nil,
			rejectedHandoffRoutingCommand,
			[]session.RecipientRole{session.RecipientHandoff}, []session.DeliveryStatus{session.DeliveryFailed}, true},
		{"invalid plan", session.RoutingParticipantSend, nil, nil,
			func(*session.Session) session.Command { return session.SendToParticipantCommand{} },
			[]session.RecipientRole{session.RecipientPrimary}, []session.DeliveryStatus{session.DeliveryNotAttempted}, true},
	}
}

func broadcastRoutingCommand(*session.Session) session.Command {
	return session.BroadcastCommand{Aliases: []string{"ada", "turing"}, Text: "work"}
}
func outsideRoomRoutingCommand(*session.Session) session.Command {
	return session.SendToParticipantOutsideRoomCommand{Alias: "ada", Text: "unrecorded"}
}
func handoffRoutingCommand(*session.Session) session.Command {
	return session.HandoffCommand{FromAlias: "ada", ToAlias: "turing", Source: session.HandoffSource{Text: "context"}}
}
func rejectedHandoffRoutingCommand(*session.Session) session.Command {
	return session.HandoffCommand{FromAlias: "ada", ToAlias: "turing"}
}
