package interpreter

import (
	"fmt"

	"github.com/trigosec/coderoom/internal/participant"
	"github.com/trigosec/coderoom/internal/room"
	"github.com/trigosec/coderoom/internal/session"
	"github.com/trigosec/coderoom/internal/shell"
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

type effect interface{ effect() }

type executeSessionEffect struct {
	target  workflowRef
	request sessionRequest
}

type startShellEffect struct {
	target  workflowRef
	request shellRequest
}

type planSharedSendEffect struct {
	target workflowRef
	alias  string
}

type readParticipantStateEffect struct{ target workflowRef }

type appendRecordEffect struct{ record room.Record }
type publishEventEffect struct{ event Event }

func (executeSessionEffect) effect()       {}
func (startShellEffect) effect()           {}
func (planSharedSendEffect) effect()       {}
func (readParticipantStateEffect) effect() {}
func (appendRecordEffect) effect()         {}
func (publishEventEffect) effect()         {}

type effectBatch struct {
	effects         []effect
	publishSnapshot bool
}

func (b *effectBatch) append(next effectBatch) {
	b.effects = append(b.effects, next.effects...)
	b.publishSnapshot = b.publishSnapshot || next.publishSnapshot
}

type sessionRequest interface{ sessionRequest() }

type planAndExecuteSharedSendRequest struct {
	alias         string
	directText    string
	listenersText string
}

type executePlannedSharedSendRequest struct {
	plan          session.SharedSendPlan
	directText    string
	listenersText string
}

type broadcastRequest struct{ text string }

type handoffRequest struct {
	fromAlias   string
	toAlias     string
	idleAliases []string
	source      session.HandoffSource
}

type cancelRequest struct{ alias string }

func (planAndExecuteSharedSendRequest) sessionRequest() {}
func (executePlannedSharedSendRequest) sessionRequest() {}
func (broadcastRequest) sessionRequest()                {}
func (handoffRequest) sessionRequest()                  {}
func (cancelRequest) sessionRequest()                   {}

type participantState struct {
	alias  string
	status participant.Status
	turnID uint64
}

type participantStateResult struct {
	barrier  []participantState
	routable []participantState
}

type sharedSendPlanResult struct {
	plan    session.SharedSendPlan
	targets []string
}

type shellRequest struct {
	command string
	program string
}

type workflowCompletion interface{ workflowCompletion() }

type sessionCompletion struct {
	target workflowRef
	err    error
}

type shellCompletion struct {
	target  workflowRef
	request shellRequest
	result  shell.Result
	cwd     string
}

type sharedSendPlanCompletion struct {
	target workflowRef
	result sharedSendPlanResult
}

type participantStateCompletion struct {
	target workflowRef
	result participantStateResult
}

func (sessionCompletion) workflowCompletion()          {}
func (shellCompletion) workflowCompletion()            {}
func (sharedSendPlanCompletion) workflowCompletion()   {}
func (participantStateCompletion) workflowCompletion() {}

type executorItem interface{ executorItem() }
type effectItem struct{ effect effect }
type completionItem struct{ completion workflowCompletion }

func (effectItem) executorItem()     {}
func (completionItem) executorItem() {}

type workflowShellCompletedOperation struct {
	target  workflowRef
	request shellRequest
	result  shell.Result
}

func (op workflowShellCompletedOperation) apply(i *Interpreter) {
	i.applyEffectBatch(i.workflows.handleCompletion(shellCompletion{
		target:  op.target,
		request: op.request,
		result:  op.result,
		cwd:     i.cwd,
	}))
}

func effectItems(effects []effect) []executorItem {
	items := make([]executorItem, len(effects))
	for index, effect := range effects {
		items[index] = effectItem{effect: effect}
	}
	return items
}

func (i *Interpreter) applyEffectBatch(batch effectBatch) {
	queue := effectItems(batch.effects)
	publishSnapshot := batch.publishSnapshot
	for len(queue) > 0 {
		item := queue[0]
		queue = queue[1:]
		switch item := item.(type) {
		case effectItem:
			follow, snapshot := i.applyEffect(item.effect)
			queue = append(follow, queue...)
			publishSnapshot = publishSnapshot || snapshot
		case completionItem:
			follow := i.workflows.handleCompletion(item.completion)
			queue = append(effectItems(follow.effects), queue...)
			publishSnapshot = publishSnapshot || follow.publishSnapshot
		default:
			panic(fmt.Sprintf("unknown executor item %T", item))
		}
	}
	if publishSnapshot {
		i.publish(StateChanged{Snapshot: i.captureSnapshot()})
	}
}

func (i *Interpreter) applyEffect(value effect) ([]executorItem, bool) {
	switch value := value.(type) {
	case appendRecordEffect:
		i.room.AppendRecord(value.record)
	case publishEventEffect:
		i.publish(value.event)
	case startShellEffect:
		i.startWorkflowShell(value)
	case executeSessionEffect:
		return i.executeSessionEffect(value)
	case planSharedSendEffect:
		plan := i.session.PlanSharedSend(value.alias)
		return []executorItem{completionItem{completion: sharedSendPlanCompletion{
			target: value.target,
			result: sharedSendPlanResult{plan: plan, targets: plan.Targets()},
		}}}, false
	case readParticipantStateEffect:
		return []executorItem{completionItem{completion: participantStateCompletion{
			target: value.target, result: i.readParticipantState(),
		}}}, false
	default:
		panic(fmt.Sprintf("unknown workflow effect %T", value))
	}
	return nil, false
}

func (i *Interpreter) executeSessionEffect(value executeSessionEffect) ([]executorItem, bool) {
	err := i.executeSessionRequest(value.request)
	events := i.takeSessionEvents(false)
	eventBatch := i.projectSessionEvents(events)
	items := effectItems(eventBatch.effects)
	items = append(items, completionItem{completion: sessionCompletion{target: value.target, err: err}})
	return items, eventBatch.publishSnapshot
}

func (i *Interpreter) executeSessionRequest(request sessionRequest) error {
	switch request := request.(type) {
	case planAndExecuteSharedSendRequest:
		return i.executeSessionCommand(session.SharedSendCommand{
			Plan:          i.session.PlanSharedSend(request.alias),
			TextDirect:    request.directText,
			TextListeners: request.listenersText,
		})
	case executePlannedSharedSendRequest:
		return i.executeSessionCommand(session.SharedSendCommand{
			Plan: request.plan, TextDirect: request.directText, TextListeners: request.listenersText,
		})
	case broadcastRequest:
		return i.executeSessionCommand(session.BroadcastCommand{Text: request.text})
	case handoffRequest:
		return i.executeSessionCommand(session.HandoffCommand{
			FromAlias: request.fromAlias, ToAlias: request.toAlias,
			IdleAliases: request.idleAliases, Source: request.source,
		})
	case cancelRequest:
		return i.executeSessionCommand(session.CancelCommand{Alias: request.alias})
	default:
		return fmt.Errorf("unsupported session request %T", request)
	}
}

// executeSessionCommand deliberately preserves the original session error.
// Workflows own user-facing context, and delivery errors carry partial-success
// metadata that must cross the gateway unchanged.
func (i *Interpreter) executeSessionCommand(command session.Command) error {
	return i.session.Execute(command) //nolint:wrapcheck // Preserve the workflow boundary contract described above.
}

func (i *Interpreter) readParticipantState() participantStateResult {
	return participantStateResult{
		barrier:  detachedParticipantState(i.session.BarrierParticipants()),
		routable: detachedParticipantState(i.session.RoutableParticipants()),
	}
}

func detachedParticipantState(values []participant.Participant) []participantState {
	states := make([]participantState, len(values))
	for index, value := range values {
		states[index] = participantState{alias: value.Alias, status: value.Status, turnID: value.TurnID()}
	}
	return states
}

func (i *Interpreter) startWorkflowShell(value startShellEffect) {
	i.shellWG.Add(1)
	go func() {
		defer i.shellWG.Done()
		result := i.runShell.Run(i.lifetime, i.cwd, value.request.program)
		i.enqueue(workflowShellCompletedOperation{
			target: value.target, request: value.request, result: result,
		})
	}()
}
