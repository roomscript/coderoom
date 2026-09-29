package interpreter

import (
	"github.com/trigosec/coderoom/internal/promptlang"
	"github.com/trigosec/coderoom/internal/session"
)

func (*interpreterModel) submitCancel(raw string, cancel promptlang.Cancel) instructionSequence {
	return sessionSubmissionSequence(raw, "cancel", session.CancelCommand{Alias: cancel.Alias})
}
