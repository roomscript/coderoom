package std

import (
	"errors"
	"fmt"
	"slices"

	"github.com/roomscript/coderoom/internal/interpreter/runtime"
	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/promptlang"
	"github.com/roomscript/coderoom/internal/session"
)

// SendCommand prepares addressed sends with frozen delivery recipients.
type SendCommand struct{}

var _ runtime.Command = SendCommand{}

// Name identifies the command in the catalog.
func (SendCommand) Name() string { return "send" }

// Usage supplies the command's help example.
func (SendCommand) Usage() string { return "@<alias> <text>" }

// Description supplies the command's help description.
func (SendCommand) Description() string { return "send to one agent" }

// Statement identifies the accepted parsed statement.
func (SendCommand) Statement() promptlang.Statement { return promptlang.Send{} }

// Prepare resolves the target through Session and selects recipients once.
// The invocation retains that delivery plan; staging describes its readiness needs.
func (SendCommand) Prepare(parsed promptlang.ParsedStatement, ctx runtime.Context) (runtime.Invocation, error) {
	statement, err := validateSendInput(parsed, ctx)
	if err != nil {
		return nil, err
	}
	alias, err := resolveSendTarget(statement.Alias.Value, ctx.Session)
	if err != nil {
		return nil, fmt.Errorf("prepare send: %w", err)
	}
	plan := ctx.Session.CreateParticipantSendPlan(alias)
	if len(plan.Targets()) == 0 {
		return nil, errors.New("send requires a valid participant send plan")
	}
	return newSendInvocation(statement, parsed.Raw, plan, ctx.Session), nil
}

func validateSendInput(parsed promptlang.ParsedStatement, ctx runtime.Context) (promptlang.Send, error) {
	statement, ok := parsed.Value.(promptlang.Send)
	if !ok {
		return promptlang.Send{}, errors.New("send requires a send statement")
	}
	if ctx.Session == nil {
		return promptlang.Send{}, errors.New("send requires session execution")
	}
	if parsed.Raw == "" {
		return promptlang.Send{}, errors.New("send requires original input")
	}
	if ctx.Participants == nil {
		return promptlang.Send{}, errors.New("send requires participant reads")
	}
	return statement, nil
}

// Missing and crashed targets fail preparation. Busy and starting targets are
// valid: the lifecycle waits for readiness, and Session checks again at delivery.
func resolveSendTarget(name string, execution runtime.SessionExecution) (participant.Alias, error) {
	view, ok := execution.ParticipantByName(name)
	if !ok {
		return participant.Alias{}, fmt.Errorf("participant %q not found: %w", name, &runtime.RequiredParticipantUnavailableError{Alias: participant.Alias{Name: name}})
	}
	if view.Status == participant.StatusCrashed {
		return participant.Alias{}, fmt.Errorf("participant %q is unavailable: %w", name, &runtime.RequiredParticipantUnavailableError{Alias: view.Alias})
	}
	return view.Alias, nil
}

func newSendInvocation(statement promptlang.Send, raw string, plan session.ParticipantSendPlan, execution runtime.SessionExecution) *sendInvocation {
	invocation := &sendInvocation{
		session: execution,
		staging: deriveSendStaging(plan),
		command: session.SendToParticipantCommand{
			Plan: plan, Message: statement.Text.Value,
			Notice: fmt.Sprintf("@%s: %s", statement.Alias.Value, statement.Text.Value),
		},
		raw: raw,
	}
	invocation.staging.DiscardInform = invocation.discardInform
	return invocation
}

func deriveSendStaging(plan session.ParticipantSendPlan) *runtime.Staging {
	primary := plan.PrimaryParticipant()
	notices := slices.DeleteFunc(plan.Targets(), func(alias participant.Alias) bool { return alias == primary })
	return &runtime.Staging{WaitFor: []participant.Alias{primary}, ToInform: notices}
}

// sendInvocation owns the retained send plan and command policy. Delivery owns
// launch/completion/close mechanics; the interpreter drives its lifecycle.
type sendInvocation struct {
	session  runtime.SessionExecution
	staging  *runtime.Staging
	command  session.SendToParticipantCommand
	raw      string
	delivery runtime.Delivery
}

func (i *sendInvocation) Staging() *runtime.Staging { return i.staging }

func (i *sendInvocation) discardInform(alias participant.Alias) {
	i.delivery.UpdatePrepared(func() {
		i.command.Plan = i.command.Plan.DiscardUnavailableNoticeRecipients([]participant.Alias{alias})
	})
}

func (i *sendInvocation) Go(complete func(runtime.CommandResult)) error {
	primary := i.command.Plan.PrimaryParticipant()
	if err := i.delivery.Go(i.session, i.command, func(result session.RoutingResult, err error) {
		if err == nil && !validSendOutcome(primary, result) {
			err = runtime.ErrInvalidDeliveryOutcome
		}
		complete(deriveDeliveryCompletion(i.raw, result, err))
	}); err != nil {
		return fmt.Errorf("send delivery: %w", err)
	}
	return nil
}

func validSendOutcome(primary participant.Alias, result session.RoutingResult) bool {
	return result.Kind == session.RoutingParticipantSend && slices.ContainsFunc(result.Recipients, func(recipient session.RecipientResult) bool {
		return recipient.Role == session.RecipientPrimary && recipient.Alias == primary
	})
}

func (i *sendInvocation) Close() { i.delivery.Close() }
