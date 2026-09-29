// Package interpreter implements coderoom's UI-independent application layer.
package interpreter

import (
	"context"
	"sync"

	"github.com/trigosec/coderoom/internal/participant"
	"github.com/trigosec/coderoom/internal/promptlang"
	"github.com/trigosec/coderoom/internal/queue"
	"github.com/trigosec/coderoom/internal/session"
	"github.com/trigosec/coderoom/internal/shell"
)

// Option configures an Interpreter.
type Option func(*Interpreter)

type operation interface{ apply(*Interpreter) }

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
type shellCompletedOperation struct {
	command string
	result  shell.Result
}
type shutdownOperation struct{}

// Interpreter serializes application operations and session dispatch.
type Interpreter struct {
	session  SessionController
	model    *interpreterModel
	runner   *instructionRunner
	cwd      string
	runShell ShellRunner
	shellWG  sync.WaitGroup

	operations *queue.Queue[operation]
	dispatcher *eventDispatcher
	done       chan struct{}
	lifetime   context.Context
	cancel     context.CancelFunc
	closeOnce  sync.Once
	enqueueMu  sync.Mutex
	closed     bool

	stateMu     sync.RWMutex
	approval    *Approval
	sessionDown bool

	sessionInbox sessionEventInbox
}

// New starts an Interpreter backed by sess.
func New(ctx context.Context, sess SessionController, cwd string, opts ...Option) *Interpreter {
	if ctx == nil {
		ctx = context.Background()
	}
	lifetime, cancel := context.WithCancel(ctx)
	i := &Interpreter{
		session:    sess,
		model:      newInterpreterModel(),
		cwd:        cwd,
		runShell:   ShellRunnerFunc(shell.Run),
		operations: queue.New[operation](),
		dispatcher: newEventDispatcher(),
		done:       make(chan struct{}),
		lifetime:   lifetime,
		cancel:     cancel,
	}
	for _, opt := range opts {
		opt(i)
	}
	i.runner = newInstructionRunner(i.model, i)
	sess.AddObserver(sessionObserver{interpreter: i})
	go i.run()
	go func() {
		select {
		case <-lifetime.Done():
			i.requestClose()
		case <-i.done:
		}
	}()
	return i
}

// ResolveCommand returns a room-scoped command body. It returns ErrClosed if
// shutdown prevents acceptance.
func (i *Interpreter) ResolveCommand(invocation promptlang.CommandInvocation) (promptlang.Shell, error) {
	result := make(chan resolveCommandResult, 1)
	if !i.enqueue(resolveCommandOperation{invocation: invocation, result: result}) {
		return promptlang.Shell{}, ErrClosed
	}
	select {
	case resolved := <-result:
		return resolved.body, resolved.err
	case <-i.done:
		select {
		case resolved := <-result:
			return resolved.body, resolved.err
		default:
			return promptlang.Shell{}, ErrClosed
		}
	}
}

// ExecuteLegacy synchronously executes a transitional session command on the
// interpreter loop. It returns ErrClosed if shutdown prevents acceptance.
func (i *Interpreter) ExecuteLegacy(command session.Command) error {
	result := make(chan error, 1)
	if !i.enqueue(executeLegacyOperation{command: command, result: result}) {
		return ErrClosed
	}
	select {
	case err := <-result:
		return err
	case <-i.done:
		select {
		case err := <-result:
			return err
		default:
			return ErrClosed
		}
	}
}

// Snapshot returns a detached view of current application state.
func (i *Interpreter) Snapshot() Snapshot {
	result := make(chan Snapshot, 1)
	if !i.enqueue(snapshotOperation{result: result}) {
		return i.captureSnapshot()
	}
	select {
	case snapshot := <-result:
		return snapshot
	case <-i.done:
		return i.captureSnapshot()
	}
}

// AddObserver registers an application event observer.
func (i *Interpreter) AddObserver(observer Observer) {
	i.dispatcher.AddObserver(observer)
}

// Close stops the interpreter and its owned background work.
func (i *Interpreter) Close() {
	i.requestClose()
	<-i.done
}

func (i *Interpreter) requestClose() {
	i.closeOnce.Do(func() {
		i.enqueueMu.Lock()
		i.closed = true
		i.operations.Push(shutdownOperation{})
		i.enqueueMu.Unlock()
	})
}

func (i *Interpreter) enqueue(op operation) bool {
	i.enqueueMu.Lock()
	defer i.enqueueMu.Unlock()
	if i.closed {
		return false
	}
	i.operations.Push(op)
	return true
}

func (i *Interpreter) run() {
	for {
		op, ok := i.operations.Pull()
		if !ok {
			return
		}
		i.applySessionEvents()
		op.apply(i)
		if _, stopping := op.(shutdownOperation); stopping {
			return
		}
		i.applySessionEvents()
	}
}

func (drainSessionEventsOperation) apply(i *Interpreter) {
	i.drainSessionEvents()
}

func (i *Interpreter) recordSessionEvent(event session.Event) {
	if i.sessionInbox.Record(event) {
		i.enqueue(drainSessionEventsOperation{})
	}
}

func (i *Interpreter) applySessionEvents() {
	for {
		events := i.sessionInbox.Take()
		if len(events) == 0 {
			return
		}
		i.runner.Run(i.runner.ApplySessionEvents(events))
	}
}

func (i *Interpreter) drainSessionEvents() {
	for {
		events := i.sessionInbox.Take()
		if len(events) != 0 {
			i.runner.Run(i.runner.ApplySessionEvents(events))
			continue
		}
		if i.sessionInbox.CompleteDrain() {
			return
		}
	}
}

func (i *Interpreter) takeSessionEvents() []session.Event {
	return i.sessionInbox.Take()
}

func (i *Interpreter) applyApprovalEvent(event session.Event) bool {
	switch event := event.(type) {
	case session.ApprovalRequested:
		approval := approvalFromAgent(event.ID, event.Alias, event.Req)
		i.stateMu.Lock()
		i.approval = &approval
		i.stateMu.Unlock()
	case session.ApprovalCleared:
		return i.clearApproval(event.ID)
	}
	return true
}

func (op executeLegacyOperation) apply(i *Interpreter) {
	err := i.session.Execute(op.command)
	i.applySessionEvents()
	op.result <- err
}

func (i *Interpreter) executeCommand(command session.Command) error {
	// Preserve session error identity and partial-delivery metadata across the
	// executor boundary; the model owns presentation and error classification.
	return i.session.Execute(command) //nolint:wrapcheck
}

func (i *Interpreter) roster() []participant.View {
	return append([]participant.View(nil), i.session.Roster()...)
}

func (op snapshotOperation) apply(i *Interpreter) {
	op.result <- i.captureSnapshot()
}

func (op resolveCommandOperation) apply(i *Interpreter) {
	body, err := i.model.ResolveCommand(op.invocation)
	op.result <- resolveCommandResult{body: body, err: err}
}

func (shutdownOperation) apply(i *Interpreter) {
	i.shutdownSession()
	i.cancel()
	i.shellWG.Wait()
	i.operations.Close()
	i.model.Close()
	i.dispatcher.Close()
	close(i.done)
}

func (i *Interpreter) shutdownSession() {
	if i.sessionDown {
		return
	}
	i.sessionDown = true
	i.session.Shutdown()
}

func (i *Interpreter) captureSnapshot() Snapshot {
	snapshot := Snapshot{
		Room:         i.model.Snapshot(),
		Participants: append([]participant.View(nil), i.session.Roster()...),
	}
	i.stateMu.RLock()
	defer i.stateMu.RUnlock()
	if i.approval != nil {
		approval := *i.approval
		approval.Options = append([]ApprovalOption(nil), approval.Options...)
		snapshot.Approval = &approval
	}
	return snapshot
}

func (i *Interpreter) publish(event Event) {
	i.dispatcher.Publish(event)
}
