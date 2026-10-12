package interpreter

import (
	"sync"

	"github.com/roomscript/coderoom/internal/agent"
	"github.com/roomscript/coderoom/internal/agent/echo"
	roomconfig "github.com/roomscript/coderoom/internal/config"
	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/policy"
	"github.com/roomscript/coderoom/internal/session"
)

type aliasFixtureSet struct {
	aliases map[string]participant.Alias
	plans   map[string]session.ParticipantSendPlan
}

var aliasFixtures = sync.OnceValue(func() aliasFixtureSet {
	s := session.New(session.WithAgentFactory(func(*session.Session, roomconfig.ParticipantConfig, session.AgentBackend) agent.Agent {
		return echo.New()
	}))
	for _, name := range []string{"ada", "ben", "cat", "turing", "grace", "tim", "bob", "later", "linus", "replacement"} {
		if err := s.Execute(session.InviteCommand{Alias: name}); err != nil {
			panic(err)
		}
	}
	if err := s.Execute(session.EnablePolicyCommand{Name: policy.SendNotices}); err != nil {
		panic(err)
	}
	fixtures := aliasFixtureSet{aliases: map[string]participant.Alias{}, plans: map[string]session.ParticipantSendPlan{}}
	for _, view := range s.Participants() {
		fixtures.aliases[view.Alias.Name] = view.Alias
		fixtures.plans[view.Alias.Name] = s.CreateParticipantSendPlan(view.Alias)
	}
	s.Shutdown()
	return fixtures
})

// Legacy workflow fixtures share immutable instances; replacement tests create their own sessions.
func testAlias(name string) participant.Alias {
	if alias, ok := aliasFixtures().aliases[name]; ok {
		return alias
	}
	return participant.Alias{Name: name}
}

func testAliases(names []string) []participant.Alias {
	if names == nil {
		return nil
	}
	result := make([]participant.Alias, len(names))
	for i, name := range names {
		result[i] = testAlias(name)
	}
	return result
}

func testSendPlan(names []string) session.ParticipantSendPlan {
	if len(names) == 0 {
		return session.ParticipantSendPlan{}
	}
	plan, ok := aliasFixtures().plans[names[0]]
	if !ok {
		return session.ParticipantSendPlan{}
	}
	var discard []participant.Alias
	for _, alias := range plan.Targets()[1:] {
		keep := false
		for _, name := range names[1:] {
			if alias.Name == name {
				keep = true
			}
		}
		if !keep {
			discard = append(discard, alias)
		}
	}
	return plan.DiscardUnavailableNoticeRecipients(discard)
}
