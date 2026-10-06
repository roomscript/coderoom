package interpreter

import (
	"github.com/roomscript/coderoom/internal/promptlang"
	"github.com/roomscript/coderoom/internal/session"
)

func (*interpreterModel) submitRemove(raw string, remove promptlang.Remove) instructionSequence {
	return sessionSubmissionSequence(raw, "remove", session.RemoveCommand{Alias: remove.Alias})
}
