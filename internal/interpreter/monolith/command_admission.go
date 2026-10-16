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
	session   *commandSession
	shell     *userShellLauncher
}

// AdmitCommand checks admission and supplies invocation-scoped capabilities.
// Call only from Schedule. Nil means rejection was published; the caller must
// not execute. The caller owns input acceptance and outcome policy.
func (i *Interpreter) AdmitCommand(name string, statement promptlang.ParsedStatement) *CommandSubmission {
	e := i.executor
	if rejection := e.model.CheckInputAllowed(statement.Raw); len(rejection) != 0 {
		e.runner.Run(rejection)
		return nil
	}
	delivery := &commandSession{executor: e}
	launcher := &userShellLauncher{executor: e}
	return &CommandSubmission{
		Context: runtime.Context{
			Participants: participantViews(e.participants()), Session: delivery, Shell: launcher,
			History: e.model, CompletedExecutions: e.model.Room(), UserCommands: i.model.commands,
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

// PublishCompletion publishes an already-settled result. The caller consumes
// duplicate/late results and closes its lifecycle before calling this method.
func (s *CommandSubmission) PublishCompletion(result runtime.CommandResult, events ...Event) {
	s.Close()
	outcome := submissionOutcome{raw: s.Statement.Raw, operation: s.Name, statement: s.Statement}
	sequence := invocationCompletionSequence(outcome, result, s.shell.completionEvent(outcome, result.Records))
	for _, event := range events {
		sequence = append(sequence, publishEventInstruction{event: event})
	}
	s.session.publishCompletion(withSubmissionSource(sequence, s.Statement), len(result.Records))
}

// Close revokes native admission without publishing an
// outcome. The new interpreter uses it when a waiting invocation is edited or
// discarded. Lifecycle-owned participant subscriptions are closed by its owner.
func (s *CommandSubmission) Close() { s.session.close() }

// ExecuteInput executes legacy input on the shared lane, after the new
// interpreter has applied its admission policy. Call only from Schedule.
func (i *Interpreter) ExecuteInput(raw string) { i.executor.handleInput(raw) }

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

// WithStageSnapshot installs a detached stage projection owned by the new
// interpreter. It runs on the shared lane; nil preserves the legacy projection.
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

// The following legacy stage operations run on the shared lane. The new
// interpreter calls them only when it has no stage of its own.

// TakeLegacyStageForEdit removes the monolith's retained draft.
func (i *Interpreter) TakeLegacyStageForEdit() (string, bool) {
	sequence, raw, ok := i.executor.model.TakeStageForEdit()
	i.executor.runner.Run(sequence)
	return raw, ok
}

// DiscardLegacyStage abandons the monolith's retained draft.
func (i *Interpreter) DiscardLegacyStage() bool {
	sequence, ok := i.executor.model.DiscardStage()
	i.executor.runner.Run(sequence)
	return ok
}

// InterruptLegacyStage requests cancellation for the monolith's current stage.
func (i *Interpreter) InterruptLegacyStage() bool {
	sequence, ok := i.executor.model.InterruptAndDispatchStage()
	i.executor.runner.Run(sequence)
	return ok
}

// WithUserCommands shares the interpreter-owned definition registry with legacy
// loop and resolution callers. Remove this bridge when those consumers migrate.
func WithUserCommands(commands *promptlang.Registry) Option {
	return func(i *Interpreter) { i.model.commands = commands }
}
