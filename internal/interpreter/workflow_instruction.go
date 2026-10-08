package interpreter

import (
	"fmt"
	"slices"

	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/room"
	"github.com/roomscript/coderoom/internal/session"
	"github.com/roomscript/coderoom/internal/shell"
)

type workflowKind uint8

const (
	workflowLoop workflowKind = iota + 1
	workflowStage
)

type workflowRef struct {
	kind       workflowKind
	generation uint64
	requestID  uint64
}

type instruction interface{ instruction() }

type executeSessionInstruction struct {
	target  workflowRef
	request sessionRequest
	// recordsOnSuccess are applied on acceptance (including partial delivery)
	// before causal events; non-routing commands require a nil execution error.
	recordsOnSuccess []room.Record
}

type startShellInstruction struct {
	target  workflowRef
	request shellRequest
}

type executeCommandInstruction struct {
	command    session.Command
	completion submissionCompletion
}

type startUserShellInstruction struct {
	raw     string
	command string
	program string
}

type readRosterInstruction struct{ raw string }
type createParticipantSendPlanInstruction struct {
	target workflowRef
	alias  string
}
type planBroadcastInstruction struct{ target workflowRef }
type readParticipantStateInstruction struct{ target workflowRef }
type readHandoffSourceInstruction struct {
	target workflowRef
	alias  string
}
type publishSnapshotInstruction struct{}
type requestSnapshotInstruction struct{}
type requestCloseInstruction struct{}
type shutdownSessionInstruction struct{}

type appendRecordInstruction struct{ record room.Record }
type publishEventInstruction struct{ event Event }

func (executeSessionInstruction) instruction()            {}
func (startShellInstruction) instruction()                {}
func (executeCommandInstruction) instruction()            {}
func (startUserShellInstruction) instruction()            {}
func (readRosterInstruction) instruction()                {}
func (createParticipantSendPlanInstruction) instruction() {}
func (planBroadcastInstruction) instruction()             {}
func (readParticipantStateInstruction) instruction()      {}
func (readHandoffSourceInstruction) instruction()         {}
func (publishSnapshotInstruction) instruction()           {}
func (requestSnapshotInstruction) instruction()           {}
func (requestCloseInstruction) instruction()              {}
func (shutdownSessionInstruction) instruction()           {}
func (appendRecordInstruction) instruction()              {}
func (publishEventInstruction) instruction()              {}

type instructionSequence []instruction

func (s *instructionSequence) append(next instructionSequence) {
	*s = append(*s, next...)
}

type sessionRequest interface{ sessionRequest() }

type createPlanAndExecuteParticipantSendRequest struct {
	alias   string
	message string
	notice  string
}

type executePlannedParticipantSendRequest struct {
	plan    session.ParticipantSendPlan
	message string
	notice  string
}

type broadcastRequest struct {
	aliases []string
	text    string
}

type cancelRequest struct{ alias string }

type handoffRequest struct {
	fromAlias            string
	toAlias              string
	requiredReadyAliases []string
	source               session.HandoffSource
}

func (createPlanAndExecuteParticipantSendRequest) sessionRequest() {}
func (executePlannedParticipantSendRequest) sessionRequest()       {}
func (broadcastRequest) sessionRequest()                           {}
func (cancelRequest) sessionRequest()                              {}
func (handoffRequest) sessionRequest()                             {}

type shellRequest struct {
	command string
	program string
}

type workflowCompletion interface{ workflowCompletion() }

type sessionCompletion struct {
	routing session.RoutingResult
	target  workflowRef
	err     error
}

type shellCompletion struct {
	target  workflowRef
	request shellRequest
	result  shell.Result
	cwd     string
}

type submissionCompletion struct {
	raw       string
	operation string
	err       error
}

type rosterCompletion struct {
	raw          string
	participants []participant.View
}

type participantSendPlanResult struct {
	target  workflowRef
	plan    session.ParticipantSendPlan
	targets []string
}

type broadcastPlanResult struct {
	target  workflowRef
	targets []string
}

type participantState struct {
	alias          string
	status         participant.Status
	turnID         uint64
	startupPending bool
}

func (s participantState) view() participant.View {
	return participant.View{Alias: s.alias, Status: s.status, TurnID: s.turnID, StartupReady: !s.startupPending}
}

type participantStateResult struct {
	target                workflowRef
	readinessRequirements []participantState
}

type handoffSourceResult struct {
	target workflowRef
	source session.HandoffSource
	ok     bool
}

func (sessionCompletion) workflowCompletion()         {}
func (shellCompletion) workflowCompletion()           {}
func (submissionCompletion) workflowCompletion()      {}
func (rosterCompletion) workflowCompletion()          {}
func (participantSendPlanResult) workflowCompletion() {}
func (broadcastPlanResult) workflowCompletion()       {}
func (participantStateResult) workflowCompletion()    {}
func (handoffSourceResult) workflowCompletion()       {}

type executorItem interface{ executorItem() }
type instructionItem struct{ instruction instruction }
type completionItem struct{ completion workflowCompletion }
type sessionEventsItem struct{ events []session.Event }

func (instructionItem) executorItem()   {}
func (completionItem) executorItem()    {}
func (sessionEventsItem) executorItem() {}

type workflowShellCompletedOperation struct {
	target  workflowRef
	request shellRequest
	result  shell.Result
}

func (op workflowShellCompletedOperation) apply(e *interpreterExecutor) {
	e.runner.Run(e.model.ApplyCompletion(shellCompletion{
		target:  op.target,
		request: op.request,
		result:  op.result,
		cwd:     e.cwd,
	}))
}

func instructionItems(instructions instructionSequence) []executorItem {
	items := make([]executorItem, len(instructions))
	for index, instruction := range instructions {
		items[index] = instructionItem{instruction: instruction}
	}
	return items
}

func (e *interpreterExecutor) executeSessionRequest(request sessionRequest) error {
	switch request := request.(type) {
	case createPlanAndExecuteParticipantSendRequest:
		err := e.session.Execute(session.SendToParticipantCommand{
			Plan:    e.session.CreateParticipantSendPlan(request.alias),
			Message: request.message,
			Notice:  request.notice,
		})
		if err != nil {
			return fmt.Errorf("execute shared send: %w", err)
		}
		return nil
	case executePlannedParticipantSendRequest:
		err := e.session.Execute(session.SendToParticipantCommand{
			Plan:    request.plan,
			Message: request.message,
			Notice:  request.notice,
		})
		if err != nil {
			return fmt.Errorf("execute planned shared send: %w", err)
		}
		return nil
	case broadcastRequest:
		return e.executeBroadcastRequest(request)
	case cancelRequest:
		return e.executeCancelRequest(request)
	case handoffRequest:
		if err := e.session.Execute(session.HandoffCommand{
			FromAlias: request.fromAlias, ToAlias: request.toAlias,
			RequiredReadyAliases: slices.Clone(request.requiredReadyAliases), Source: request.source,
		}); err != nil {
			return fmt.Errorf("execute handoff: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("unsupported session request %T", request)
	}
}

func (e *interpreterExecutor) executeBroadcastRequest(request broadcastRequest) error {
	if err := e.session.Execute(session.BroadcastCommand{
		Aliases: slices.Clone(request.aliases), Text: request.text,
	}); err != nil {
		return fmt.Errorf("execute broadcast: %w", err)
	}
	return nil
}

func (e *interpreterExecutor) executeCancelRequest(request cancelRequest) error {
	if err := e.session.Execute(session.CancelCommand{Alias: request.alias}); err != nil {
		return fmt.Errorf("cancel %q: %w", request.alias, err)
	}
	return nil
}

func (e *interpreterExecutor) startWorkflowShell(value startShellInstruction) {
	e.shellWG.Add(1)
	go func() {
		defer e.shellWG.Done()
		result := e.runShell.Run(e.lifetime, e.cwd, value.request.program)
		e.enqueue(workflowShellCompletedOperation{
			target: value.target, request: value.request, result: result,
		})
	}()
}
