package interpreter

import (
	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/promptlang"
	"github.com/roomscript/coderoom/internal/session"
)

// handoffStage owns its action, source-completion requirement and accepted
// context. Shared participant readiness remains owned by stageRequirements.
type handoffStage struct {
	action                promptlang.Handoff
	sourceNeedsCompletion bool
	completed             *session.HandoffDelivered
}

func (h *handoffStage) captureCompletion(event session.Event) {
	delivered, ok := event.(session.HandoffDelivered)
	if !ok || delivered.FromAlias != h.action.FromAlias.Value || delivered.ToAlias != h.action.ToAlias.Value {
		return
	}
	h.completed = &delivered
}

func (h *handoffStage) trackSourceCompletion(participants []participantState) {
	for _, value := range participants {
		if value.alias == h.action.FromAlias.Value {
			h.sourceNeedsCompletion = value.view().HasActiveTurn()
			return
		}
	}
}

func (h *handoffStage) updateSourceStatus(event session.ParticipantStatusChanged) {
	if event.Alias == h.action.FromAlias.Value && (participant.View{Status: event.To}).HasActiveTurn() {
		h.sourceNeedsCompletion = true
	}
}

func (h *handoffStage) completeSourceTurn(event session.AgentMessage, expectedTurnID uint64) bool {
	if event.Alias != h.action.FromAlias.Value || !event.TurnCompleted || event.TurnID < expectedTurnID {
		return false
	}
	h.sourceNeedsCompletion = false
	return true
}

func (h *handoffStage) routing() []string {
	if h.action.FromAlias.Value == h.action.ToAlias.Value {
		return []string{h.action.FromAlias.Value}
	}
	return []string{h.action.FromAlias.Value, h.action.ToAlias.Value}
}

func (h *handoffStage) deliveryRequest(source session.HandoffSource, requiredReadyAliases []string) handoffRequest {
	return handoffRequest{
		fromAlias: h.action.FromAlias.Value, toAlias: h.action.ToAlias.Value,
		requiredReadyAliases: requiredReadyAliases, source: source,
	}
}
