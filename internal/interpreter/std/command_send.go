package std

import (
	"errors"
	"fmt"
	"slices"
	"sync"

	"github.com/roomscript/coderoom/internal/interpreter/runtime"
	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/promptlang"
	"github.com/roomscript/coderoom/internal/room"
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

type sendState uint8

const (
	sendPrepared sendState = iota
	sendAwaitingDelivery
	sendClosed
)

type sendInvocation struct {
	mu          sync.Mutex
	session     runtime.SessionExecution
	state       sendState
	unsubscribe func()
	staging     *runtime.Staging
	command     session.SendToParticipantCommand
	raw         string
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

func (i *sendInvocation) Staging() *runtime.Staging { return i.staging }

// discardInform changes the frozen delivery plan only before execution starts.
func (i *sendInvocation) discardInform(alias participant.Alias) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.state != sendPrepared {
		return
	}
	i.command.Plan = i.command.Plan.DiscardUnavailableNoticeRecipients([]participant.Alias{alias})
}

// Go subscribes before admitting delivery. Admission errors return here;
// delivery outcomes arrive through the subscription and enqueue completion.
func (i *sendInvocation) Go(complete func(runtime.CommandResult)) error {
	if err := i.subscribeForDelivery(complete); err != nil {
		return err
	}
	if err := i.session.Execute(i.command); err != nil {
		i.Close()
		return fmt.Errorf("launch send: %w", err)
	}
	return nil
}

func (i *sendInvocation) subscribeForDelivery(complete func(runtime.CommandResult)) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.state != sendPrepared {
		return runtime.ErrInvocationNotPrepared
	}
	i.state = sendAwaitingDelivery
	i.unsubscribe = i.session.Subscribe(func(event session.Event) {
		i.handleSessionEvent(event, complete)
	})
	if i.unsubscribe == nil {
		i.unsubscribe = func() {}
	}
	return nil
}

func (i *sendInvocation) handleSessionEvent(event session.Event, complete func(runtime.CommandResult)) {
	routed, ok := event.(session.RoutingCompleted)
	if !ok {
		return
	}
	unsubscribe := i.acceptDelivery(routed.Result)
	if unsubscribe == nil {
		return
	}
	unsubscribe()
	complete(deriveSendCompletion(i.raw, routed.Result))
}

// acceptDelivery checks ownership and consumes the result under one lock.
// Closing before releasing the lock rejects duplicates and racing callbacks.
// Nil means rejection. Otherwise, the caller releases the accepted result's
// subscription after unlocking; subscribeForDelivery ensures a non-nil function.
func (i *sendInvocation) acceptDelivery(result session.RoutingResult) func() {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.state != sendAwaitingDelivery || !i.matchesDelivery(result) {
		return nil
	}
	i.state = sendClosed
	unsubscribe := i.unsubscribe
	i.unsubscribe = nil
	return unsubscribe
}

// matchesDelivery is called with the invocation locked. A notice to our alias
// does not identify our send; the result must identify our primary instance.
func (i *sendInvocation) matchesDelivery(result session.RoutingResult) bool {
	if result.Kind != session.RoutingParticipantSend {
		return false
	}
	primary := i.command.Plan.PrimaryParticipant()
	return slices.ContainsFunc(result.Recipients, func(recipient session.RecipientResult) bool {
		return recipient.Role == session.RecipientPrimary && recipient.Alias == primary
	})
}

// deriveSendCompletion preserves actual partial routing and original input.
// No submitted input record is created when delivery reaches nobody.
func deriveSendCompletion(raw string, result session.RoutingResult) runtime.CommandResult {
	completion := runtime.CommandResult{Err: result.Err}
	delivered := result.Aliases(session.DeliveryDelivered)
	if len(delivered) == 0 {
		return completion
	}
	completion.Records = []room.Record{{
		Kind: room.KindUserInput, Text: raw, Routing: delivered,
		FailedRouting: result.Aliases(session.DeliveryFailed),
		UnsentRouting: result.Aliases(session.DeliveryNotAttempted),
	}}
	return completion
}

// Close rejects future starts and delivery results, then releases the subscription
// once. Unsubscribe runs outside the lock so callbacks can finish safely.
func (i *sendInvocation) Close() {
	i.mu.Lock()
	i.state = sendClosed
	unsubscribe := i.unsubscribe
	i.unsubscribe = nil
	i.mu.Unlock()
	if unsubscribe != nil {
		unsubscribe()
	}
}
