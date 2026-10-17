package std

import (
	"errors"
	"fmt"

	"github.com/roomscript/coderoom/internal/interpreter/runtime"
	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/promptlang"
	"github.com/roomscript/coderoom/internal/session"
)

// LoopCommand coordinates immediate participant delivery and a frozen condition.
// It deliberately has no staging; Session owns execution-time availability.
type LoopCommand struct{}

// Name identifies the command in the catalog.
func (LoopCommand) Name() string { return "loop" }

// Usage supplies the command’s help example.
func (LoopCommand) Usage() string { return "/loop @<alias> <prompt> /until /<name> /max <turns>" }

// Description supplies the command’s help description.
func (LoopCommand) Description() string { return "run a bounded participant loop" }

// Statement identifies the accepted parsed statement.
func (LoopCommand) Statement() promptlang.Statement { return promptlang.Loop{} }

// Prepare freezes the condition body and participant instance for one invocation.
func (LoopCommand) Prepare(parsed promptlang.ParsedStatement, ctx runtime.Context) (runtime.Invocation, error) {
	statement, ok := parsed.Value.(promptlang.Loop)
	if !ok {
		return nil, errors.New("loop requires a loop statement")
	}
	if err := validateLoopCapabilities(ctx); err != nil {
		return nil, err
	}
	if statement.MaxTurns.Value < 1 {
		return nil, errors.New("loop requires a positive turn bound")
	}
	definition, err := ctx.NamedCommands.Resolve(promptlang.UserCommand{Name: statement.Condition})
	if err != nil {
		return nil, fmt.Errorf("loop condition /%s: %w", statement.Condition.Value, err)
	}
	body, err := promptlang.RequireShell(definition)
	if err != nil {
		return nil, fmt.Errorf("loop condition /%s: %w", statement.Condition.Value, err)
	}
	view, ok := ctx.Session.ParticipantByName(statement.Participant.Value)
	if !ok {
		return nil, fmt.Errorf("loop participant %q not found", statement.Participant.Value)
	}
	return &loopInvocation{ctx: ctx, raw: parsed.Raw, statement: statement, primary: view.Alias, program: body.Value.Program.Value, work: make(map[session.ExecutionID]bool)}, nil
}

type loopPhase uint8

const (
	loopDispatching loopPhase = iota
	loopWaiting
	loopEvaluating
	loopFinished
)

type loopInvocation struct {
	ctx               runtime.Context
	raw               string
	statement         promptlang.Loop
	primary           participant.Alias
	program           string
	phase             loopPhase
	turns             int
	acknowledged      bool
	executionID       session.ExecutionID
	activeExecutionID session.ExecutionID
	// Presence means this execution ended; true means an actual completion that
	// must also reach room history. False denotes rollback without a terminal completion.
	work        map[session.ExecutionID]bool
	departure   string
	delivery    *runtime.Delivery
	unsubscribe func()
	complete    func(runtime.CommandResult)
	events      loopEvents
}

var (
	_ runtime.Command                   = LoopCommand{}
	_ runtime.InvocationEventHandler    = (*loopInvocation)(nil)
	_ runtime.SubmissionAcknowledgement = (*loopInvocation)(nil)
)

func (*loopInvocation) Staging() *runtime.Staging  { return nil }
func (i *loopInvocation) ReadyToAcknowledge() bool { return i.acknowledged }
func (i *loopInvocation) Go(complete func(runtime.CommandResult)) error {
	i.complete = complete
	snapshot, unsubscribe := i.ctx.Session.SubscribeParticipants(i.collectParticipantEvent)
	i.unsubscribe = unsubscribe
	for _, view := range snapshot.Participants {
		if view.Alias == i.primary {
			i.activeExecutionID = view.ActiveExecutionID
		}
	}
	return i.startDelivery(i.statement.Prompt.Value)
}

func (i *loopInvocation) startDelivery(prompt string) error {
	i.phase = loopDispatching
	i.executionID = 0
	clear(i.work)
	i.delivery = &runtime.Delivery{}
	command := session.SendToParticipantCommand{
		Plan: i.ctx.Session.CreateParticipantSendPlan(i.primary), Message: prompt,
		Notice: fmt.Sprintf("@%s: %s", i.statement.Participant.Value, prompt),
	}
	if err := i.delivery.Go(i.ctx.Session.CreateExecution(), command, func(result runtime.ExecutionCompleted, err error) {
		i.collect(loopDeliveryResult{result: result, err: err})
	}); err != nil {
		return fmt.Errorf("loop delivery: %w", err)
	}
	return nil
}

func (i *loopInvocation) acceptDelivery(outcome loopDeliveryResult) {
	if i.phase != loopDispatching {
		return
	}
	if outcome.err != nil {
		i.finish("", outcome.err)
		return
	}
	result := outcome.result
	if result.ExecutionID == 0 || !validSendOutcome(i.primary, result.Result) {
		i.finish("", runtime.ErrInvalidDeliveryOutcome)
		return
	}
	i.acknowledged = true
	if !loopPrimaryDelivered(i.primary, result.Result) {
		i.finish("[loop] stopped: participant turn could not start", nil)
		return
	}
	if i.departure != "" {
		i.finish(i.departure, nil)
		return
	}
	i.executionID = result.ExecutionID
	i.turns++
	i.phase = loopWaiting
	i.publishStatus(fmt.Sprintf("[loop] turn %d/%d sent to @%s", i.turns, i.statement.MaxTurns.Value, i.primary.Name))
}

func loopPrimaryDelivered(primary participant.Alias, result session.RoutingResult) bool {
	for _, recipient := range result.Recipients {
		if recipient.Alias == primary && recipient.Role == session.RecipientPrimary && recipient.Status == session.DeliveryDelivered {
			return true
		}
	}
	return false
}

func (i *loopInvocation) finish(message string, err error) {
	if i.phase == loopFinished {
		return
	}
	i.phase = loopFinished
	if message != "" {
		i.publishStatus(message)
	}
	i.complete(runtime.CommandResult{Err: err})
}

func (i *loopInvocation) Close() {
	i.events.close()
	if i.unsubscribe != nil {
		i.unsubscribe()
		i.unsubscribe = nil
	}
	if i.delivery != nil {
		i.delivery.Close()
	}
}

func validateLoopCapabilities(ctx runtime.Context) error {
	if ctx.Session == nil || ctx.Shell == nil || ctx.NamedCommands == nil || ctx.EventProcessing == nil || ctx.Publication == nil || ctx.CompletedExecutions == nil {
		return errors.New("loop requires delivery, shell, definitions, event processing, publication and completion history")
	}
	return nil
}
