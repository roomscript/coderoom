package monolith

import (
	"github.com/roomscript/coderoom/internal/interpreter/runtime"
	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/promptlang"
	"github.com/roomscript/coderoom/internal/room"
	"github.com/roomscript/coderoom/internal/session"
)

// Schedule queues work on the single execution/publication lane. The caller owns
// that work's orchestration. Accepted work runs before shutdown; new work returns
// ErrClosed once shutdown starts. Work must not call blocking queue APIs such as
// Snapshot or Close on this interpreter. Native sends admitted by the work
// dispatch before the next scheduled turn inspects participant readiness.
func (i *Interpreter) Schedule(work func()) error {
	if !i.executor.enqueue(scheduledOperation{work: work}) {
		return ErrClosed
	}
	return nil
}

type scheduledOperation struct{ work func() }

func (op scheduledOperation) apply(e *interpreterExecutor) {
	op.work()
	e.dispatchNativeDeliveries()
}

// WithShutdown installs a cleanup hook on the serialized lane after admitted
// execution and queued completions drain, before publication closes. The caller
// retains ownership of its workflows; this hook also runs on context cancellation.
func WithShutdown(closeWork func()) Option {
	return func(i *Interpreter) { i.executor.onShutdown = closeWork }
}

// CommandSubmission is admission metadata and invocation-scoped capabilities,
// not a lifecycle. Its publication methods run only on the serialized lane.
// QueueCompletion is safe for producer goroutines.
type CommandSubmission struct {
	Context   runtime.Context
	executor  *interpreterExecutor
	Statement promptlang.ParsedStatement
	Name      string
	session   *commandSessionProvider
	shell     *userShellLauncher
}

// AdmitCommand supplies invocation-scoped capabilities for already-admitted input.
// Call only from Schedule. The caller owns stage admission, input acceptance
// and outcome policy. Legacy workflow state does not gate modular execution.
func (i *Interpreter) AdmitCommand(name string, statement promptlang.ParsedStatement) *CommandSubmission {
	e := i.executor
	delivery := &commandSessionProvider{executor: e}
	launcher := &userShellLauncher{executor: e}
	return &CommandSubmission{
		Context: runtime.Context{
			Participants: participantViews(e.participants()), Session: delivery, Shell: launcher,
			History: e.model, CompletedExecutions: e.model.Room(), NamedCommands: i.model.commands,
		},
		executor: e, Statement: statement, Name: name,
		session: delivery, shell: launcher,
	}
}

// Publish publishes a caller-selected event with the submission's source metadata.
func (s *CommandSubmission) Publish(event Event) {
	s.executor.runner.Run(withSubmissionSource(instructionSequence{
		publishEventInstruction{event: event},
	}, s.Statement))
}

// QueueCompletion schedules caller-owned result handling, including during
// shutdown draining. Routing callbacks retain it in their causal delivery burst
// so accepted input records can precede the session events caused by delivery.
func (s *CommandSubmission) QueueCompletion(work func()) {
	s.session.queueCompletionWork(work)
}

// PublishRecords publishes explicit output without closing execution scopes or
// synthesizing shell completion events. Call only on the interpreter lane. During
// native delivery these updates follow the complete causal Session event burst.
func (s *CommandSubmission) PublishRecords(records []room.Record, events ...Event) {
	sequence := invocationPublicationSequence(records, events...)
	s.session.publishOutput(withSubmissionSource(sequence, s.Statement), 0)
}

// PublishCompletion publishes an already-settled result. The caller consumes
// duplicate/late results and closes its lifecycle before calling this method.
func (s *CommandSubmission) PublishCompletion(result runtime.CommandResult, events ...Event) {
	s.Close()
	outcome := submissionOutcome{raw: s.Statement.Raw, operation: s.Name, statement: s.Statement}
	sequence := invocationCompletionSequence(outcome, result, s.shell.completionEvent(outcome, result.Records))
	for _, event := range events {
		sequence = append(sequence, publishEventInstruction{event: event})
	}
	s.session.publishOutput(withSubmissionSource(sequence, s.Statement), len(result.Records))
}

// Close revokes native admission without publishing an
// outcome. The new interpreter uses it when a waiting invocation is edited or
// discarded. Lifecycle-owned participant subscriptions are closed by its owner.
func (s *CommandSubmission) Close() { s.session.close() }

// PublishState refreshes and publishes the single canonical snapshot on the lane.
func (i *Interpreter) PublishState() {
	i.executor.runner.Run(instructionSequence{publishSnapshotInstruction{}})
}

// Publish queues a semantic application event on the serialized lane.
func (i *Interpreter) Publish(event Event) {
	i.executor.runner.Run(instructionSequence{publishEventInstruction{event: event}})
}

// AppendRecord publishes a canonical record on the serialized lane.
func (i *Interpreter) AppendRecord(record room.Record) {
	i.executor.runner.Run(instructionSequence{appendRecordInstruction{record: record}})
}

// WithStageSnapshot installs the authoritative detached stage projection.
// It runs on the shared lane; a nil result means no stage.
func WithStageSnapshot(snapshot func() *StagedSubmission) Option {
	return func(i *Interpreter) { i.executor.stageSnapshot = snapshot }
}

// CancelParticipant executes cancellation on the shared lane. Its causal facts
// are projected before the caller handles the result; success does not mean idle.
func (s *CommandSubmission) CancelParticipant(alias participant.Alias) error {
	err := s.executor.executeCommand(session.CancelCommand{Alias: alias})
	s.executor.applySessionEvents()
	return err //nolint:wrapcheck // Preserve cancellation error identity.
}

// WithNamedCommands shares the interpreter-owned definition registry with legacy
// loop and resolution callers. Remove this bridge when those consumers migrate.
func WithNamedCommands(commands *promptlang.Registry) Option {
	return func(i *Interpreter) { i.model.commands = commands }
}

// QueuePublication queues application dispatch after earlier observer publications.
// Call from the serialized lane; callbacks must return promptly and only queue
// results back to their owner. Shutdown drains accepted work before settlement.
// This temporary mechanism moves with publication when the monolith is removed.
func (i *Interpreter) QueuePublication(work func()) {
	i.executor.dispatcher.QueueWork(work)
}
