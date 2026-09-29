package interpreter

import (
	"context"
	"sync"

	"github.com/trigosec/coderoom/internal/agent"
	"github.com/trigosec/coderoom/internal/participant"
	"github.com/trigosec/coderoom/internal/promptlang"
	"github.com/trigosec/coderoom/internal/queue"
	"github.com/trigosec/coderoom/internal/session"
	"github.com/trigosec/coderoom/internal/shell"
)

type operation interface{ apply(*interpreterExecutor) }

type drainSessionEventsOperation struct{}
type executeLegacyOperation struct {
	command session.Command
	result  chan error
}
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
	Submit(string, promptlang.Statement, session.Command) instructionSequence
	ApplyShellResult(string, string, shell.Result) instructionSequence
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

func (e *interpreterExecutor) Start() {
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

func (e *interpreterExecutor) executeLegacy(command session.Command) error {
	result := make(chan error, 1)
	if !e.enqueue(executeLegacyOperation{command: command, result: result}) {
		return ErrClosed
	}
	select {
	case err := <-result:
		return err
	case <-e.done:
		select {
		case err := <-result:
			return err
		default:
			return ErrClosed
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

func (e *interpreterExecutor) addObserver(observer Observer) {
	e.dispatcher.AddObserver(observer)
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

func (op executeLegacyOperation) apply(e *interpreterExecutor) {
	err := e.session.Execute(op.command)
	e.applySessionEvents()
	op.result <- err
}

func (e *interpreterExecutor) executeCommand(command session.Command) error {
	// Preserve session error identity and partial-delivery metadata across the
	// executor boundary; the model owns presentation and error classification.
	return e.session.Execute(command) //nolint:wrapcheck
}

func (e *interpreterExecutor) roster() []participant.View {
	return append([]participant.View(nil), e.session.Roster()...)
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
		Participants: append([]participant.View(nil), e.session.Roster()...),
		Approval:     model.approval,
	}
	e.snapshots.Store(snapshot)
	return cloneSnapshot(snapshot)
}

func (e *interpreterExecutor) publish(event Event) {
	e.dispatcher.Publish(event)
}
