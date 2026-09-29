package interpreter

import (
	"github.com/trigosec/coderoom/internal/promptlang"
	"github.com/trigosec/coderoom/internal/session"
)

func (*interpreterModel) submitPolicyEnable(raw string, enable promptlang.PolicyEnable) instructionSequence {
	return sessionSubmissionSequence(raw, "policy", session.EnablePolicyCommand{Name: enable.Name})
}
