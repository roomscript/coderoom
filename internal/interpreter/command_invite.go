package interpreter

import (
	"github.com/trigosec/coderoom/internal/promptlang"
	"github.com/trigosec/coderoom/internal/session"
)

func (*interpreterModel) submitInvite(raw string, invite promptlang.Invite) instructionSequence {
	return sessionSubmissionSequence(raw, "invite", session.InviteCommand{Alias: invite.Alias})
}
