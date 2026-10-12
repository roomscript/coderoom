package interpreter

import (
	"github.com/roomscript/coderoom/internal/promptlang"
)

func (*interpreterModel) submitCancel(raw string, cancel promptlang.Cancel) instructionSequence {
	return append(acceptedInputSequence(raw), executeParticipantCommandInstruction{
		name: cancel.Alias.Value, remove: false, outcome: submissionOutcome{raw: raw, operation: "cancel"},
	})
}
