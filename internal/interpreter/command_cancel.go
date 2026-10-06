package interpreter

import (
	"github.com/roomscript/coderoom/internal/promptlang"
	"github.com/roomscript/coderoom/internal/session"
)

func (*interpreterModel) submitCancel(raw string, cancel promptlang.Cancel) instructionSequence {
	return sessionSubmissionSequence(raw, "cancel", session.CancelCommand{Alias: cancel.Alias})
}
