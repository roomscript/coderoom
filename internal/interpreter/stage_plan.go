package interpreter

import (
	"fmt"
	"slices"

	"github.com/roomscript/coderoom/internal/promptlang"
	"github.com/roomscript/coderoom/internal/session"
)

// stagePlan holds the frozen recipients and their requirements. Retaining this
// plan preserves the original selection while readiness changes.
type stagePlan struct {
	routing      []string
	requirements stageRequirements
	send         *sendPlan
}

// sendPlan keeps the addressed action and session delivery plan together.
// Broadcast and handoff preparation remain separate checkpoints.
type sendPlan struct {
	action   promptlang.Send
	delivery session.ParticipantSendPlan
}

func (p *sendPlan) deliveryRequest(unavailable []string) executePlannedParticipantSendRequest {
	return executePlannedParticipantSendRequest{
		plan:    p.delivery.DiscardUnavailableNoticeRecipients(unavailable),
		message: p.action.Text,
		notice:  fmt.Sprintf("@%s: %s", p.action.Alias, p.action.Text),
	}
}

func (p *stagePlan) freezeSend(result sendPlanResult) {
	p.send.delivery = result.plan
	p.routing = slices.Clone(result.targets)
	p.requirements = freezeStageRequirements(participantsForRouting(p.routing, result.participants), p.routing)
}

func participantsForRouting(routing []string, participants []participantState) []participantState {
	byAlias := make(map[string]participantState, len(participants))
	for _, value := range participants {
		byAlias[value.alias] = value
	}
	readinessRequirements := make([]participantState, 0, len(routing))
	for _, alias := range routing {
		if value, ok := byAlias[alias]; ok {
			readinessRequirements = append(readinessRequirements, value)
		}
	}
	return readinessRequirements
}
