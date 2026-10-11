package std

import (
	"errors"
	"fmt"

	"github.com/roomscript/coderoom/internal/interpreter/runtime"
	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/promptlang"
	"github.com/roomscript/coderoom/internal/room"
	"github.com/roomscript/coderoom/internal/session"
)

// SendCommand prepares addressed sends. This prototype is not registered in the
// interpreter; delivery and staging integration remain separate checkpoints.
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

// Prepare retains Session's frozen send plan and derives readiness requirements.
func (SendCommand) Prepare(parsed promptlang.ParsedStatement, ctx runtime.Context) (runtime.Invocation, error) {
	statement, ok := parsed.Value.(promptlang.Send)
	if !ok {
		return nil, errors.New("send requires a send statement")
	}
	if ctx.Session == nil {
		return nil, errors.New("send requires session execution")
	}
	if parsed.Raw == "" {
		return nil, errors.New("send requires original input")
	}
	if ctx.Participants == nil {
		return nil, errors.New("send requires participant reads")
	}
	if err := validateSendTarget(statement.Alias.Value, ctx.Participants.Participants()); err != nil {
		return nil, fmt.Errorf("prepare send: %w", err)
	}
	plan := ctx.Session.CreateParticipantSendPlan(statement.Alias.Value)
	targets := plan.Targets()
	if len(targets) == 0 {
		return nil, errors.New("send requires a valid participant send plan")
	}
	return &sendInvocation{
		session: ctx.Session,
		staging: &runtime.Staging{WaitFor: targets[:1], ToInform: targets[1:]},
		command: session.SendToParticipantCommand{
			Plan: plan, Message: statement.Text.Value,
			Notice: fmt.Sprintf("@%s: %s", statement.Alias.Value, statement.Text.Value),
		},
		raw: parsed.Raw,
	}, nil
}

// Validation preserves preparation-time rejection of missing or crashed targets.
// Session remains the authority for selection and execution-time availability.
func validateSendTarget(alias string, participants []participant.View) error {
	for _, view := range participants {
		if view.Alias != alias {
			continue
		}
		if view.Status == participant.StatusCrashed {
			return fmt.Errorf("participant %q is unavailable", alias)
		}
		return nil
	}
	return fmt.Errorf("participant %q not found", alias)
}

type sendInvocation struct {
	session     runtime.SessionExecution
	unsubscribe func()
	closed      bool
	staging     *runtime.Staging
	command     session.SendToParticipantCommand
	raw         string
}

func (i *sendInvocation) Staging() *runtime.Staging { return i.staging }

// Go registers its continuation before admitting delivery. The host queues
// events, so even immediate delivery cannot resume this invocation inside Go.
func (i *sendInvocation) Go(complete func(runtime.CommandResult)) error {
	if i.closed || i.unsubscribe != nil {
		return runtime.ErrInvocationNotPrepared
	}
	i.unsubscribe = i.session.Subscribe(func(event session.Event) {
		i.handleSessionEvent(event, complete)
	})
	if err := i.session.Execute(i.command); err != nil {
		i.Close()
		return fmt.Errorf("launch send: %w", err)
	}
	return nil
}

func (i *sendInvocation) handleSessionEvent(event session.Event, complete func(runtime.CommandResult)) {
	if i.closed {
		return
	}
	routed, ok := event.(session.RoutingCompleted)
	if !ok || routed.Result.Kind != session.RoutingParticipantSend {
		return
	}
	i.Close()
	result := routed.Result
	completion := runtime.CommandResult{Err: result.Err}
	delivered := result.Aliases(session.DeliveryDelivered)
	if len(delivered) != 0 {
		completion.Records = []room.Record{{
			Kind: room.KindUserInput, Text: i.raw, Routing: delivered,
			FailedRouting: result.Aliases(session.DeliveryFailed),
			UnsentRouting: result.Aliases(session.DeliveryNotAttempted),
		}}
	}
	complete(completion)
}

// Close releases the continuation on completion, failed admission or discard.
func (i *sendInvocation) Close() {
	if i.closed {
		return
	}
	i.closed = true
	if i.unsubscribe != nil {
		i.unsubscribe()
		i.unsubscribe = nil
	}
}
