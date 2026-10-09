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
	broadcast    *promptlang.Broadcast
}

// sendPlan keeps the addressed action and session delivery plan together.
// Handoff source selection remains a separate checkpoint.
type sendPlan struct {
	action   promptlang.Send
	delivery session.ParticipantSendPlan
}

func (p *sendPlan) deliveryRequest(unavailable []string) executePlannedParticipantSendRequest {
	return executePlannedParticipantSendRequest{
		plan:    p.delivery.DiscardUnavailableNoticeRecipients(unavailable),
		message: p.action.Text.Value,
		notice:  fmt.Sprintf("@%s: %s", p.action.Alias.Value, p.action.Text.Value),
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

func (p *stagePlan) freezeBroadcastRequirements(participants []participantState) {
	p.requirements = freezeStageRequirements(participantsForRouting(p.routing, participants), p.routing)
}

func (p *stagePlan) broadcastDeliveryRequest() broadcastRequest {
	return broadcastRequest{aliases: activeAliases(p.routing, p.requirements.unavailable), text: p.broadcast.Text.Value}
}

func (p *stagePlan) freezeHandoffRequirements(routing []string, participants []participantState) {
	p.routing = slices.Clone(routing)
	participants = slices.DeleteFunc(slices.Clone(participants), func(value participantState) bool {
		explicit := slices.Contains(p.routing, value.alias)
		return !explicit && (!value.view().IsRoutable() || value.startupPending)
	})
	p.requirements = freezeStageRequirements(participants, p.routing)
}
