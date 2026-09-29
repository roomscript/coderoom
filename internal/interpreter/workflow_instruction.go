package interpreter

import (
	"fmt"

	"github.com/trigosec/coderoom/internal/participant"
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

type instruction interface{ instruction() }

type executeSessionInstruction struct {
	target  workflowRef
	request sessionRequest
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
type publishSnapshotInstruction struct{}
type requestSnapshotInstruction struct{}
type requestCloseInstruction struct{}
type shutdownSessionInstruction struct{}

type appendRecordInstruction struct{ record room.Record }
type publishEventInstruction struct{ event Event }

func (executeSessionInstruction) instruction()  {}
func (startShellInstruction) instruction()      {}
func (executeCommandInstruction) instruction()  {}
func (startUserShellInstruction) instruction()  {}
func (readRosterInstruction) instruction()      {}
func (publishSnapshotInstruction) instruction() {}
func (requestSnapshotInstruction) instruction() {}
func (requestCloseInstruction) instruction()    {}
func (shutdownSessionInstruction) instruction() {}
func (appendRecordInstruction) instruction()    {}
func (publishEventInstruction) instruction()    {}

type instructionSequence []instruction

func (s *instructionSequence) append(next instructionSequence) {
	*s = append(*s, next...)
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

type submissionCompletion struct {
	raw       string
	operation string
	err       error
}

type rosterCompletion struct {
	raw          string
	participants []participant.View
}

func (sessionCompletion) workflowCompletion()    {}
func (shellCompletion) workflowCompletion()      {}
func (submissionCompletion) workflowCompletion() {}
func (rosterCompletion) workflowCompletion()     {}

type executorItem interface{ executorItem() }
type instructionItem struct{ instruction instruction }
type completionItem struct{ completion workflowCompletion }

func (instructionItem) executorItem() {}
func (completionItem) executorItem()  {}

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
	case planAndExecuteSharedSendRequest:
		err := e.session.Execute(session.SharedSendCommand{
			Plan:          e.session.PlanSharedSend(request.alias),
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
