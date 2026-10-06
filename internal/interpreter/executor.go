package interpreter

import (
	"context"
	"slices"
	"sync"

	"github.com/roomscript/coderoom/internal/agent"
	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/promptlang"
	"github.com/roomscript/coderoom/internal/queue"
	"github.com/roomscript/coderoom/internal/session"
	"github.com/roomscript/coderoom/internal/shell"
)

type operation interface{ apply(*interpreterExecutor) }

type drainSessionEventsOperation struct{}
type snapshotOperation struct{ result chan Snapshot }
type resolveCommandResult struct {
	body promptlang.Shell
	err  error
}
type resolveCommandOperation struct {
	invocation promptlang.CommandInvocation
	result     chan resolveCommandResult
}
type shutdownOperation struct{}

type executorModelPort interface {
	instructionModelPort
	PreflightSubmission(string) instructionSequence
	Submit(string, promptlang.Statement) instructionSequence
	ApplyShellResult(string, string, shell.Result) instructionSequence
	TakeStageForEdit() (instructionSequence, string, bool)
	DiscardStage() (instructionSequence, bool)
	InterruptAndDispatchStage() (instructionSequence, bool)
	ResolveApprovalChoice(int64, ApprovalChoice) (agent.ApprovalOption, error)
	ClearApproval(int64) bool
	ResolveCommand(promptlang.CommandInvocation) (promptlang.Shell, error)
	Snapshot() modelSnapshot
	Close()
}

// interpreterExecutor owns serialized execution, asynchronous work, event
// ingestion and delivery, and interpreter shutdown.
type interpreterExecutor struct {
	session   SessionController
	model     executorModelPort
	runner    *instructionRunner
	snapshots snapshotCache
	cwd       string
	runShell  ShellRunner
	shellWG   sync.WaitGroup

	operations *queue.Queue[operation]
	dispatcher *eventDispatcher
	inbox      sessionEventInbox
	done       chan struct{}
	lifetime   context.Context
	cancel     context.CancelFunc
	closeOnce  sync.Once
	enqueueMu  sync.Mutex
	closed     bool

	sessionDown bool
}

func newInterpreterExecutor(
	ctx context.Context,
	sess SessionController,
	cwd string,
	model *interpreterModel,
) *interpreterExecutor {
	lifetime, cancel := context.WithCancel(ctx)
	executor := &interpreterExecutor{
		session:    sess,
		model:      model,
		cwd:        cwd,
		runShell:   ShellRunnerFunc(shell.Run),
		operations: queue.New[operation](),
		dispatcher: newEventDispatcher(),
		done:       make(chan struct{}),
		lifetime:   lifetime,
		cancel:     cancel,
	}
	executor.runner = newInstructionRunner(model, executor)
	executor.refreshSnapshot()
	return executor
}

func (e *interpreterExecutor) start() {
	e.publish(StateChanged{Snapshot: e.snapshots.Load()})
	e.session.AddObserver(sessionObserver{executor: e})
	go e.run()
	go func() {
		select {
		case <-e.lifetime.Done():
			e.requestClose()
		case <-e.done:
		}
	}()
}

func (e *interpreterExecutor) resolveCommand(
	invocation promptlang.CommandInvocation,
) (promptlang.Shell, error) {
	result := make(chan resolveCommandResult, 1)
	if !e.enqueue(resolveCommandOperation{invocation: invocation, result: result}) {
		return promptlang.Shell{}, ErrClosed
	}
	select {
	case resolved := <-result:
		return resolved.body, resolved.err
	case <-e.done:
		select {
		case resolved := <-result:
			return resolved.body, resolved.err
		default:
			return promptlang.Shell{}, ErrClosed
		}
	}
}

func (e *interpreterExecutor) snapshot() Snapshot {
	result := make(chan Snapshot, 1)
	if !e.enqueue(snapshotOperation{result: result}) {
		return e.snapshots.Load()
	}
	select {
	case snapshot := <-result:
		return snapshot
	case <-e.done:
		return e.snapshots.Load()
	}
}

func (e *interpreterExecutor) close() {
	e.requestClose()
	<-e.done
}

func (e *interpreterExecutor) enqueue(op operation) bool {
	e.enqueueMu.Lock()
	defer e.enqueueMu.Unlock()
	if e.closed {
		return false
	}
	e.operations.Push(op)
	return true
}

func (e *interpreterExecutor) requestClose() {
	e.closeOnce.Do(func() {
		e.enqueueMu.Lock()
		e.closed = true
		e.operations.Push(shutdownOperation{})
		e.enqueueMu.Unlock()
	})
}

func (e *interpreterExecutor) run() {
	for {
		op, ok := e.operations.Pull()
		if !ok {
			return
		}
		e.applySessionEvents()
		op.apply(e)
		if _, stopping := op.(shutdownOperation); stopping {
			return
		}
		e.applySessionEvents()
	}
}

func (drainSessionEventsOperation) apply(e *interpreterExecutor) {
	e.drainSessionEvents()
}

func (e *interpreterExecutor) recordSessionEvent(event session.Event) {
	if e.inbox.Record(event) {
		e.enqueue(drainSessionEventsOperation{})
	}
}

func (e *interpreterExecutor) applySessionEvents() {
	for {
		events := e.inbox.Take()
		if len(events) == 0 {
			return
		}
		e.runner.Run(e.runner.ApplySessionEvents(events))
	}
}

func (e *interpreterExecutor) drainSessionEvents() {
	for {
		events := e.inbox.Take()
		if len(events) != 0 {
			e.runner.Run(e.runner.ApplySessionEvents(events))
			continue
		}
		if e.inbox.CompleteDrain() {
			return
		}
	}
}

func (e *interpreterExecutor) takeSessionEvents() []session.Event {
	return e.inbox.Take()
}

func (e *interpreterExecutor) executeCommand(command session.Command) error {
	// Preserve session error identity and partial-delivery metadata across the
	// executor boundary; the model owns presentation and error classification.
	return e.session.Execute(command) //nolint:wrapcheck
}

func (e *interpreterExecutor) roster() []participant.View {
	return append([]participant.View(nil), e.session.Participants()...)
}

func (e *interpreterExecutor) planSharedSend(alias string) (session.SharedSendPlan, []string) {
	plan := e.session.PlanSharedSend(alias)
	return plan, plan.Targets()
}

func (e *interpreterExecutor) planBroadcast() []string {
	var targets []string
	for _, value := range e.session.Participants() {
		if value.Status != participant.StatusCrashed {
			targets = append(targets, value.Alias)
		}
	}
	slices.Sort(targets)
	return targets
}

func (e *interpreterExecutor) participantState() []participantState {
	return participantStates(e.session.Participants())
}

// Preserve actual lifecycle status and startup readiness in one planning read.
// Crashed participants remain present so unknown aliases can be distinguished.
func participantStates(participants []participant.View) []participantState {
	states := make([]participantState, 0, len(participants))
	for _, value := range participants {
		states = append(states, participantState{alias: value.Alias, status: value.Status, turnID: value.TurnID, startupPending: !value.StartupReady})
	}
	return states
}

func (op snapshotOperation) apply(e *interpreterExecutor) {
	op.result <- e.refreshSnapshot()
}

func (op resolveCommandOperation) apply(e *interpreterExecutor) {
	body, err := e.model.ResolveCommand(op.invocation)
	op.result <- resolveCommandResult{body: body, err: err}
}

func (shutdownOperation) apply(e *interpreterExecutor) {
	e.shutdownSession()
	e.cancel()
	e.shellWG.Wait()
	e.operations.Close()
	e.refreshSnapshot()
	e.model.Close()
	e.dispatcher.Close()
	close(e.done)
}

func (e *interpreterExecutor) shutdownSession() {
	if e.sessionDown {
		return
	}
	e.sessionDown = true
	e.session.Shutdown()
}

func (e *interpreterExecutor) refreshSnapshot() Snapshot {
	model := e.model.Snapshot()
	snapshot := Snapshot{
		Room:         model.room,
		Participants: append([]participant.View(nil), e.session.Participants()...),
		Approval:     model.approval,
		Stage:        model.stage,
	}
	e.snapshots.Store(snapshot)
	return cloneSnapshot(snapshot)
}

func (e *interpreterExecutor) publish(event Event) {
	e.dispatcher.Publish(event)
}
