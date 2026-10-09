package interpreter

import (
	"fmt"

	"github.com/roomscript/coderoom/internal/promptlang"
	"github.com/roomscript/coderoom/internal/session"
)

type loopPhase uint8

const (
	loopDispatchingParticipant loopPhase = iota
	loopWaitingForParticipant
	loopEvaluating
)

type loopState struct {
	generation             uint64
	raw                    string
	statement              promptlang.Loop
	body                   promptlang.Shell
	phase                  loopPhase
	turns                  int
	pending                workflowRef
	submissionPending      bool
	dispatchTerminalStatus string
}

func (state *loopState) participantRequest(prompt string) createPlanAndExecuteParticipantSendRequest {
	return createPlanAndExecuteParticipantSendRequest{
		alias: state.statement.Participant, message: prompt,
		notice: fmt.Sprintf("@%s: %s", state.statement.Participant, prompt),
	}
}

func (state *loopState) conditionRequest() shellRequest {
	return shellRequest{command: "/" + state.statement.Condition, program: state.body.Program}
}

func (state *loopState) participantTurnStarted(routing session.RoutingResult) bool {
	for _, recipient := range routing.Recipients {
		if recipient.Role == session.RecipientPrimary && recipient.Alias == state.statement.Participant && recipient.Status == session.DeliveryDelivered {
			return true
		}
	}
	return false
}

func (state *loopState) retainDispatchTerminalEvent(event session.Event) {
	alias := state.statement.Participant
	switch event := event.(type) {
	case session.AgentStopped:
		if event.Alias == alias {
			state.dispatchTerminalStatus = "[loop] stopped: participant @" + alias + " stopped"
		}
	case session.AgentCrashed:
		if event.Alias == alias {
			state.dispatchTerminalStatus = "[loop] stopped: participant @" + alias + " crashed"
		}
	}
}

func (state *loopState) appendSubmissionSuccess(sequence *instructionSequence) {
	if state == nil || !state.submissionPending {
		return
	}
	raw := state.raw
	state.submissionPending = false
	*sequence = append(*sequence, publishEventInstruction{event: SubmissionSucceeded{Raw: raw}})
}
