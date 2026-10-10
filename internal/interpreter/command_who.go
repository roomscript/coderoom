package interpreter

import (
	"github.com/roomscript/coderoom/internal/interpreter/runtime"
	"github.com/roomscript/coderoom/internal/interpreter/std"
	"github.com/roomscript/coderoom/internal/participant"
)

func (*interpreterModel) submitWho(raw string) instructionSequence {
	return append(acceptedInputSequence(raw), readParticipantsInstruction{raw: raw})
}

// participantViews supplies already-read facts, with no live session access.
type participantViews []participant.View

func (views participantViews) Participants() []participant.View { return views }

// participantsResultSequence keeps the experimental /who seam at legacy
// participant completion; its callback returns through the executor queue.
func participantsResultSequence(result participantsResult) instructionSequence {
	return instructionSequence{goInvocationInstruction{
		command: std.WhoCommand{},
		context: runtime.Context{Participants: participantViews(result.participants)},
		outcome: submissionOutcome{raw: result.raw, operation: "who", statement: result.statement},
	}}
}
