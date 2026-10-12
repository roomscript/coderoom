package session_test

import (
	"slices"
	"testing"

	"github.com/roomscript/coderoom/internal/agent"
	roomconfig "github.com/roomscript/coderoom/internal/config"
	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/session"
)

type identityCommandCase struct {
	name  string
	build func(*session.Session, participant.Alias) session.Command
}

func identityCommandCases() []identityCommandCase {
	return []identityCommandCase{
		{"send", func(s *session.Session, alias participant.Alias) session.Command {
			return session.SendToParticipantCommand{Plan: s.CreateParticipantSendPlan(alias), Message: "work"}
		}},
		{"outside room", func(_ *session.Session, alias participant.Alias) session.Command {
			return session.SendToParticipantOutsideRoomCommand{Alias: alias, Text: "work"}
		}},
		{"broadcast", func(_ *session.Session, alias participant.Alias) session.Command {
			return session.BroadcastCommand{Aliases: []participant.Alias{alias}, Text: "work"}
		}},
		{"handoff source", func(s *session.Session, alias participant.Alias) session.Command {
			return session.HandoffCommand{FromAlias: alias, ToAlias: resolvedAlias(s, "turing"), Source: session.HandoffSource{Text: "context"}}
		}},
		{"handoff destination", func(s *session.Session, alias participant.Alias) session.Command {
			return session.HandoffCommand{FromAlias: resolvedAlias(s, "turing"), ToAlias: alias, Source: session.HandoffSource{Text: "context"}}
		}},
		{"handoff requirement", func(s *session.Session, alias participant.Alias) session.Command {
			return session.HandoffCommand{FromAlias: resolvedAlias(s, "ada"), ToAlias: resolvedAlias(s, "turing"), RequiredReadyAliases: []participant.Alias{alias}, Source: session.HandoffSource{Text: "context"}}
		}},
		{"cancel", func(_ *session.Session, alias participant.Alias) session.Command {
			return session.CancelCommand{Alias: alias}
		}},
		{"remove", func(_ *session.Session, alias participant.Alias) session.Command {
			return session.RemoveCommand{Alias: alias}
		}},
	}
}

func TestCommands_rejectAliasesOutsideCurrentMembership(t *testing.T) {
	commands := identityCommandCases()
	for _, identity := range []string{"foreign", "replaced", "departed", "zero ID", "wrong name"} {
		for _, command := range commands {
			t.Run(identity+"/"+command.name, func(t *testing.T) {
				s, observer, adapters := newIdentityTestSession(t)
				alias := unavailableAlias(t, s, observer, identity)
				before := s.Participants()
				if err := s.Execute(command.build(s, alias)); err == nil {
					t.Fatal("command accepted an alias outside current membership")
				}
				assertNoIdentityTestDelivery(t, adapters)
				for _, view := range before {
					if after, ok := s.Participant(view.Alias); !ok || after != view {
						t.Fatalf("command changed current participant: %#v, %v", after, ok)
					}
				}
				outcomes, handoffs := readRoutingOutcomes(observer)
				if handoffs != 0 || (len(outcomes) > 0 && len(outcomes[0].Aliases(session.DeliveryDelivered)) != 0) {
					t.Fatal("rejected command reported delivery")
				}
			})
		}
	}
}

func newIdentityTestSession(t *testing.T) (*session.Session, *testObserver, []*mockAgent) {
	t.Helper()
	observer := newTestObserver()
	adapters := []*mockAgent{newMockAgent(), newMockAgent(), newMockAgent()}
	nextAda := 0
	s := newSession(t, session.WithObserver(observer), session.WithAgentFactory(func(_ *session.Session, cfg roomconfig.ParticipantConfig, _ session.AgentBackend) agent.Agent {
		if cfg.Alias == "turing" {
			return adapters[2]
		}
		a := adapters[nextAda]
		nextAda++
		return a
	}))
	for _, name := range []string{"ada", "turing"} {
		invite(t, s, name)
		mustReceive[session.AgentReady](t, observer.ch)
	}
	return s, observer, adapters
}

func unavailableAlias(t *testing.T, s *session.Session, observer *testObserver, identity string) participant.Alias {
	t.Helper()
	alias := resolvedAlias(s, "ada")
	switch identity {
	case "foreign":
		otherObserver := newTestObserver()
		other := newSession(t, session.WithObserver(otherObserver), fixedFactory(newMockAgent()))
		invite(t, other, "ada")
		mustReceive[session.AgentReady](t, otherObserver.ch)
		return resolvedAlias(other, "ada")
	case "replaced", "departed":
		if err := s.Execute(session.RemoveCommand{Alias: alias}); err != nil {
			t.Fatal(err)
		}
		mustReceive[session.AgentStopped](t, observer.ch)
		if identity == "replaced" {
			invite(t, s, "ada")
			ready := mustReceive[session.AgentReady](t, observer.ch)
			if ready.Alias.ID == alias.ID {
				t.Fatal("replacement reused participant ID")
			}
		}
	case "zero ID":
		alias.ID = participant.ID{}
	case "wrong name":
		alias.Name = "turing"
	}
	return alias
}

func assertNoIdentityTestDelivery(t *testing.T, adapters []*mockAgent) {
	t.Helper()
	for _, adapter := range adapters {
		adapter.mu.Lock()
		calls := len(adapter.sends) + adapter.interrupts
		adapter.mu.Unlock()
		if calls != 0 {
			t.Fatal("rejected alias reached an adapter")
		}
	}
}

func TestBroadcast_replacedRecipientPreservesPartialDelivery(t *testing.T) {
	s, observer, adapters := newIdentityTestSession(t)
	old := resolvedAlias(s, "ada")
	turing := resolvedAlias(s, "turing")
	command := session.BroadcastCommand{Aliases: []participant.Alias{old, turing}, Text: "work"}
	unavailableAlias(t, s, observer, "replaced")
	err := s.Execute(command)
	if err == nil || !slices.Equal(session.DeliveredAliases(err), []string{"turing"}) {
		t.Fatalf("partial delivery = %v, %v", session.DeliveredAliases(err), err)
	}
	for _, adapter := range adapters[:2] {
		adapter.mu.Lock()
		calls := len(adapter.sends)
		adapter.mu.Unlock()
		if calls != 0 {
			t.Fatal("broadcast reached the old or replacement participant")
		}
	}
	adapters[2].mu.Lock()
	defer adapters[2].mu.Unlock()
	if !slices.Equal(adapters[2].sends, []string{"work"}) {
		t.Fatalf("valid recipient sends = %v", adapters[2].sends)
	}
	outcomes, _ := readRoutingOutcomes(observer)
	if len(outcomes) != 1 || outcomes[0].Recipients[0].Alias != old || outcomes[0].Recipients[1].Alias != turing {
		t.Fatalf("routing identities = %#v", outcomes)
	}
	assertRecipientOutcomes(t, outcomes[0], []session.RecipientRole{session.RecipientBroadcast, session.RecipientBroadcast}, []session.DeliveryStatus{session.DeliveryFailed, session.DeliveryDelivered})
}
