package interpreter

import (
	"github.com/roomscript/coderoom/internal/promptlang"
)

func (*interpreterModel) submitRemove(raw string, remove promptlang.Remove) instructionSequence {
	return append(acceptedInputSequence(raw), executeParticipantCommandInstruction{
		name: remove.Alias.Value, remove: true, outcome: submissionOutcome{raw: raw, operation: "remove"},
	})
}
