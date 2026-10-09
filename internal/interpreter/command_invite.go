package interpreter

import (
	"github.com/roomscript/coderoom/internal/promptlang"
	"github.com/roomscript/coderoom/internal/session"
)

func (*interpreterModel) submitInvite(raw string, invite promptlang.Invite) instructionSequence {
	return sessionSubmissionSequence(raw, "invite", session.InviteCommand{Alias: invite.Alias.Value})
}
