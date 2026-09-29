package interpreter

import (
	"fmt"

	"github.com/trigosec/coderoom/internal/room"
	"github.com/trigosec/coderoom/internal/session"
	"github.com/trigosec/coderoom/internal/shell"
)

type workflowKind uint8

const workflowLoop workflowKind = iota + 1

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

type appendRecordEffect struct{ record room.Record }
type publishEventEffect struct{ event Event }

func (executeSessionEffect) effect() {}
func (startShellEffect) effect()     {}
func (appendRecordEffect) effect()   {}
func (publishEventEffect) effect()   {}

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

func (planAndExecuteSharedSendRequest) sessionRequest() {}

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

func (sessionCompletion) workflowCompletion() {}
func (shellCompletion) workflowCompletion()   {}

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
		err := i.session.Execute(session.SharedSendCommand{
			Plan:          i.session.PlanSharedSend(request.alias),
			TextDirect:    request.directText,
			TextListeners: request.listenersText,
		})
		if err != nil {
			return fmt.Errorf("execute shared send: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("unsupported session request %T", request)
	}
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
