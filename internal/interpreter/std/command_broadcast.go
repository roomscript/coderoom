package std

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/roomscript/coderoom/internal/interpreter/runtime"
	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/promptlang"
	"github.com/roomscript/coderoom/internal/session"
)

// BroadcastCommand sends to a frozen set of optional participant instances.
type BroadcastCommand struct{}

var _ runtime.Command = BroadcastCommand{}

// Name identifies the command in the catalog.
func (BroadcastCommand) Name() string { return "broadcast" }

// Usage supplies the command's help example.
func (BroadcastCommand) Usage() string { return "<text>" }

// Description supplies the command's help description.
func (BroadcastCommand) Description() string { return "broadcast to all agents" }

// Statement identifies the accepted parsed statement.
func (BroadcastCommand) Statement() promptlang.Statement { return promptlang.Broadcast{} }

// Prepare freezes every non-crashed recipient, including startup waits. Later
// joins cannot expand the delivery and departures remove only their own instance.
func (BroadcastCommand) Prepare(parsed promptlang.ParsedStatement, ctx runtime.Context) (runtime.Invocation, error) {
	statement, ok := parsed.Value.(promptlang.Broadcast)
	if !ok || parsed.Raw == "" {
		return nil, errors.New("broadcast requires a broadcast statement and original input")
	}
	if ctx.Session == nil || ctx.Participants == nil {
		return nil, errors.New("broadcast requires session execution and participant reads")
	}
	aliases := make([]participant.Alias, 0)
	for _, view := range ctx.Participants.Participants() {
		if view.Status != participant.StatusCrashed {
			aliases = append(aliases, view.Alias)
		}
	}
	slices.SortFunc(aliases, func(a, b participant.Alias) int { return strings.Compare(a.Name, b.Name) })
	if len(aliases) == 0 {
		return nil, runtime.ErrNoActiveTargets
	}
	invocation := &broadcastInvocation{
		session: ctx.Session,
		staging: &runtime.Staging{ToInform: slices.Clone(aliases)},
		command: session.BroadcastCommand{Aliases: aliases, Text: statement.Text.Value},
		raw:     parsed.Raw,
	}
	invocation.staging.DiscardInform = invocation.discardInform
	return invocation, nil
}

type broadcastInvocation struct {
	session  runtime.SessionExecution
	staging  *runtime.Staging
	command  session.BroadcastCommand
	raw      string
	delivery runtime.Delivery
}

func (i *broadcastInvocation) Staging() *runtime.Staging { return i.staging }

func (i *broadcastInvocation) discardInform(alias participant.Alias) {
	i.delivery.UpdatePrepared(func() {
		i.command.Aliases = slices.DeleteFunc(slices.Clone(i.command.Aliases), func(candidate participant.Alias) bool { return candidate == alias })
	})
}

func (i *broadcastInvocation) Go(complete func(runtime.CommandResult)) error {
	if len(i.command.Aliases) == 0 {
		return runtime.ErrNoActiveTargets
	}
	if err := i.delivery.Go(i.session, i.command, func(outcome runtime.DeliveryCompleted, err error) {
		result := outcome.Result
		if err == nil && !validBroadcastOutcome(i.command.Aliases, result) {
			err = runtime.ErrInvalidDeliveryOutcome
		}
		if err != nil {
			err = fmt.Errorf("execute broadcast: %w", err)
		}
		if result.Err != nil {
			result.Err = fmt.Errorf("execute broadcast: %w", result.Err)
		}
		complete(deriveDeliveryCompletion(i.raw, result, err))
	}); err != nil {
		return fmt.Errorf("broadcast delivery: %w", err)
	}
	return nil
}

func (i *broadcastInvocation) Close() { i.delivery.Close() }

func validBroadcastOutcome(aliases []participant.Alias, result session.RoutingResult) bool {
	if result.Kind != session.RoutingBroadcast || len(result.Recipients) != len(aliases) {
		return false
	}
	for index, recipient := range result.Recipients {
		if recipient.Alias != aliases[index] || recipient.Role != session.RecipientBroadcast {
			return false
		}
	}
	return true
}
