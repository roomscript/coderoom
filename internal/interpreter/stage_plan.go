package interpreter

import (
	"fmt"
	"slices"

	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/promptlang"
	"github.com/roomscript/coderoom/internal/session"
)

// stagePlan holds the frozen recipients and their requirements. Retaining this
// plan preserves the original selection while readiness changes.
type stagePlan struct {
	routing      []participant.Alias
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

func (p *sendPlan) deliveryRequest(unavailable []participant.Alias) executePlannedParticipantSendRequest {
	return executePlannedParticipantSendRequest{
		plan:    p.delivery.DiscardUnavailableNoticeRecipients(unavailable),
		message: p.action.Text.Value,
		notice:  fmt.Sprintf("@%s: %s", p.action.Alias.Value, p.action.Text.Value),
	}
}

func (p *stagePlan) freezeSend(result sendPlanResult) {
	p.send.delivery = result.plan
	targets := p.send.delivery.Targets()
	p.requirements = freezeStageRequirements(participantsForRouting(targets, result.participants), targets)
}

func participantsForRouting(routing []participant.Alias, participants []participantState) []participantState {
	byAlias := make(map[participant.Alias]participantState, len(participants))
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

func (p *stagePlan) freezeHandoffRequirements(routing []participant.Alias, participants []participantState) {
	participants = slices.DeleteFunc(slices.Clone(participants), func(value participantState) bool {
		explicit := slices.Contains(routing, value.alias)
		return !explicit && (!value.view().IsRoutable() || value.startupPending)
	})
	p.requirements = freezeStageRequirements(participants, routing)
}
