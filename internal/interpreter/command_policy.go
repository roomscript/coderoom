package interpreter

import (
	"github.com/roomscript/coderoom/internal/promptlang"
	"github.com/roomscript/coderoom/internal/session"
)

func (*interpreterModel) submitPolicyEnable(raw string, enable promptlang.PolicyEnable) instructionSequence {
	return sessionSubmissionSequence(raw, "policy", session.EnablePolicyCommand{Name: enable.Name})
}
