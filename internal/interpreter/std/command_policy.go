package std

import (
	"errors"

	"github.com/roomscript/coderoom/internal/interpreter/runtime"
	"github.com/roomscript/coderoom/internal/promptlang"
	"github.com/roomscript/coderoom/internal/session"
)

// PolicyCommand enables room-local policies through Session execution.
//
// User documentation: ../../../docs/man/policy.md
type PolicyCommand struct{}

var _ runtime.Command = PolicyCommand{}

// Name identifies the command in the catalog.
func (PolicyCommand) Name() string { return "policy" }

// Usage supplies the command's help example.
func (PolicyCommand) Usage() string { return "/policy enable <name>" }

// Description supplies the command's help description.
func (PolicyCommand) Description() string {
	return "enable send-notices (notify listeners after direct sends) or echo-invites (use deterministic echo agents)"
}

// Statement identifies the accepted parsed statement.
func (PolicyCommand) Statement() promptlang.Statement { return promptlang.PolicyEnable{} }

// Prepare captures the policy without changing Session state.
func (PolicyCommand) Prepare(parsed promptlang.ParsedStatement, ctx runtime.Context) (runtime.Invocation, error) {
	statement, ok := parsed.Value.(promptlang.PolicyEnable)
	if !ok {
		return nil, errors.New("policy requires a policy enable statement")
	}
	if ctx.Session == nil {
		return nil, errors.New("policy requires Session execution")
	}
	return &unroutedInvocation{session: ctx.Session, command: session.EnablePolicyCommand{Name: statement.Name.Value}}, nil
}
