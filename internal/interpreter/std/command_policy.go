package std

import (
	"errors"
	"fmt"

	"github.com/roomscript/coderoom/internal/interpreter/runtime"
	"github.com/roomscript/coderoom/internal/promptlang"
	"github.com/roomscript/coderoom/internal/session"
)

// PolicyCommand enables room-local policies through Session execution.
type PolicyCommand struct{}

var _ runtime.Command = PolicyCommand{}

// Name identifies the command in the catalog.
func (PolicyCommand) Name() string { return "policy" }

// Usage supplies the command's help example.
func (PolicyCommand) Usage() string { return "/policy enable <name>" }

// Description supplies the command's help description.
func (PolicyCommand) Description() string { return "enable a room-local policy" }

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
	return &policyInvocation{session: ctx.Session, command: session.EnablePolicyCommand{Name: statement.Name.Value}}, nil
}

type policyInvocation struct {
	session     runtime.SessionExecutionProvider
	command     session.EnablePolicyCommand
	unsubscribe func()
}

func (*policyInvocation) Staging() *runtime.Staging { return nil }

// ReadyToAcknowledge keeps submission pending until Session execution completes.
func (*policyInvocation) ReadyToAcknowledge() bool { return false }

func (i *policyInvocation) Go(complete func(runtime.CommandResult)) error {
	execution := i.session.CreateExecution()
	i.unsubscribe = execution.Subscribe(func(event runtime.ExecutionEvent) {
		complete(runtime.CommandResult{Err: policyExecutionError(event)})
	})
	if err := execution.Execute(i.command); err != nil {
		return fmt.Errorf("execute policy: %w", err)
	}
	return nil
}

func policyExecutionError(event runtime.ExecutionEvent) error {
	switch event := event.(type) {
	case runtime.ExecutionCompleted:
		return event.Err
	case runtime.ExecutionFailed:
		if event.Err != nil {
			return event.Err
		}
	}
	return errors.New("invalid policy execution outcome")
}

func (i *policyInvocation) Close() {
	if i.unsubscribe != nil {
		i.unsubscribe()
		i.unsubscribe = nil
	}
}
