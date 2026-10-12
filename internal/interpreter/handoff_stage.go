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
	from                  participant.Alias
	to                    participant.Alias
	sourceNeedsCompletion bool
	completed             *session.HandoffDelivered
}

func (h *handoffStage) captureCompletion(event session.Event) {
	delivered, ok := event.(session.HandoffDelivered)
	if !ok || delivered.FromAlias != h.from || delivered.ToAlias != h.to {
		return
	}
	h.completed = &delivered
}

func (h *handoffStage) trackSourceCompletion(participants []participantState) {
	for _, value := range participants {
		if value.alias == h.from {
			h.sourceNeedsCompletion = value.view().HasActiveTurn()
			return
		}
	}
}

func (h *handoffStage) updateSourceStatus(event session.ParticipantStatusChanged) {
	if event.Alias == h.from && (participant.View{Status: event.To}).HasActiveTurn() {
		h.sourceNeedsCompletion = true
	}
}

func (h *handoffStage) completeSourceTurn(event session.AgentMessage, expectedTurnID uint64) bool {
	if event.Alias != h.from || !event.TurnCompleted || event.TurnID < expectedTurnID {
		return false
	}
	h.sourceNeedsCompletion = false
	return true
}

func (h *handoffStage) routing() []participant.Alias {
	if h.from == h.to {
		return []participant.Alias{h.from}
	}
	return []participant.Alias{h.from, h.to}
}

func (h *handoffStage) deliveryRequest(source session.HandoffSource, requiredReadyAliases []participant.Alias) handoffRequest {
	return handoffRequest{
		fromAlias: h.from, toAlias: h.to,
		requiredReadyAliases: requiredReadyAliases, source: source,
	}
}
