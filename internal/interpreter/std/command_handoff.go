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

// HandoffCommand transfers completed canonical output between frozen instances.
//
// User documentation: ../../../docs/man/handoff.md
type HandoffCommand struct{}

// Name identifies the command in the catalog.
func (HandoffCommand) Name() string { return "handoff" }

// Usage supplies the command help example.
func (HandoffCommand) Usage() string { return "/handoff <from> <to>" }

// Description supplies the command help description.
func (HandoffCommand) Description() string { return "transfer latest output between agents" }

// Statement identifies the accepted parsed statement.
func (HandoffCommand) Statement() promptlang.Statement { return promptlang.Handoff{} }

// Prepare freezes the explicit source/destination and existing ready-to-route
// bystanders. Readiness prerequisites deliberately differ from routing.
func (HandoffCommand) Prepare(parsed promptlang.ParsedStatement, ctx runtime.Context) (runtime.Invocation, error) {
	statement, ok := parsed.Value.(promptlang.Handoff)
	if !ok || parsed.Raw == "" {
		return nil, errors.New("handoff requires a handoff statement and original input")
	}
	if ctx.Session == nil || ctx.Participants == nil || ctx.History == nil {
		return nil, errors.New("handoff requires execution, participant and canonical source capabilities")
	}
	from, err := resolveHandoffParticipant(statement.FromAlias.Value, ctx.Session)
	if err != nil {
		return nil, err
	}
	to, err := resolveHandoffParticipant(statement.ToAlias.Value, ctx.Session)
	if err != nil {
		return nil, err
	}
	invocation := &handoffInvocation{ctx: ctx, raw: parsed.Raw,
		command: session.HandoffCommand{FromAlias: from, ToAlias: to},
		staging: &runtime.Staging{WaitFor: []participant.Alias{from}},
	}
	if to != from {
		invocation.staging.WaitFor = append(invocation.staging.WaitFor, to)
	}
	invocation.freezeBystanders(ctx.Participants.Participants())
	invocation.staging.DiscardInform = invocation.discardBystander
	return invocation, nil
}

func resolveHandoffParticipant(name string, execution runtime.SessionExecutionProvider) (participant.Alias, error) {
	view, ok := execution.ParticipantByName(name)
	if !ok || view.Status == participant.StatusCrashed {
		return participant.Alias{}, fmt.Errorf("handoff participant %q is unavailable", name)
	}
	return view.Alias, nil
}

type handoffInvocation struct {
	ctx      runtime.Context
	raw      string
	command  session.HandoffCommand
	staging  *runtime.Staging
	delivery runtime.Delivery
}

func (i *handoffInvocation) freezeBystanders(views []participant.View) {
	i.command.RequiredReadyAliases = slices.Clone(i.staging.WaitFor)
	for _, view := range views {
		if slices.Contains(i.staging.WaitFor, view.Alias) || !view.IsRoutable() || !view.StartupReady {
			continue
		}
		i.staging.ToInform = append(i.staging.ToInform, view.Alias)
		i.command.RequiredReadyAliases = append(i.command.RequiredReadyAliases, view.Alias)
	}
}

func (i *handoffInvocation) discardBystander(alias participant.Alias) {
	i.delivery.UpdatePrepared(func() {
		i.command.RequiredReadyAliases = slices.DeleteFunc(slices.Clone(i.command.RequiredReadyAliases), func(candidate participant.Alias) bool { return candidate == alias })
	})
}

func (i *handoffInvocation) Staging() *runtime.Staging { return i.staging }
func (i *handoffInvocation) Close()                    { i.delivery.Close() }

func (i *handoffInvocation) Go(complete func(runtime.CommandResult)) error {
	source, ok := i.ctx.History.LatestOutput(i.command.FromAlias)
	if !ok {
		return errors.New("handoff source has no completed room-visible output")
	}
	i.command.Source = source
	if err := i.delivery.Go(i.ctx.Session.CreateExecution(), i.command, func(outcome runtime.ExecutionCompleted, executionErr error) {
		complete(i.completion(outcome.Result, executionErr))
	}); err != nil {
		return fmt.Errorf("handoff delivery: %w", err)
	}
	return nil
}

func (i *handoffInvocation) completion(routing session.RoutingResult, executionErr error) runtime.CommandResult {
	if executionErr == nil && !i.validOutcome(routing) {
		executionErr = runtime.ErrInvalidDeliveryOutcome
	}
	return deriveDeliveryCompletion(i.raw, routing, executionErr)
}

func (i *handoffInvocation) validOutcome(result session.RoutingResult) bool {
	return result.Kind == session.RoutingHandoff && len(result.Recipients) == 1 &&
		result.Recipients[0].Alias == i.command.ToAlias && result.Recipients[0].Role == session.RecipientHandoff
}
