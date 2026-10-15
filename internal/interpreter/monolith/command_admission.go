package monolith

import (
	"github.com/roomscript/coderoom/internal/interpreter/runtime"
	"github.com/roomscript/coderoom/internal/promptlang"
)

// Schedule queues work on the single execution/publication lane. The caller owns
// that work's orchestration. Accepted work runs before shutdown; new work returns
// ErrClosed once shutdown starts. Work must not call blocking queue APIs such as
// Snapshot or Close on this interpreter.
func (i *Interpreter) Schedule(work func()) error {
	if !i.executor.enqueue(scheduledOperation{work: work}) {
		return ErrClosed
	}
	return nil
}

type scheduledOperation struct{ work func() }

func (op scheduledOperation) apply(*interpreterExecutor) { op.work() }

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
	Context  runtime.Context
	executor *interpreterExecutor
	outcome  submissionOutcome
	session  *commandSession
	shell    *userShellLauncher
}

// AdmitCommand checks admission and publishes accepted input. Call only from
// Schedule. Nil means rejection was published; the caller must not execute.
func (i *Interpreter) AdmitCommand(name string, statement promptlang.ParsedStatement) *CommandSubmission {
	e := i.executor
	if rejection := e.model.CheckInputAllowed(statement.Raw); len(rejection) != 0 {
		e.runner.Run(rejection)
		return nil
	}
	sequence := acceptedInputSequence(statement.Raw)
	if _, send := statement.Value.(promptlang.Send); send {
		sequence = instructionSequence{publishEventInstruction{event: InputAccepted{Raw: statement.Raw}}}
	}
	e.runner.Run(withSubmissionSource(sequence, statement))
	delivery := &commandSession{executor: e}
	launcher := &userShellLauncher{executor: e}
	return &CommandSubmission{
		Context:  runtime.Context{Participants: participantViews(e.participants()), Session: delivery, Shell: launcher},
		executor: e, outcome: submissionOutcome{raw: statement.Raw, operation: name, statement: statement},
		session: delivery, shell: launcher,
	}
}

// PublishSubmission reports preparation/launch failure or acceptance. Call after
// Start returns, before processing completion work, even for immediate callbacks.
func (s *CommandSubmission) PublishSubmission(err error) {
	outcome := s.outcome
	outcome.err = err
	if err != nil {
		s.session.close()
		s.executor.runner.Run(submissionResultSequence(outcome))
		return
	}
	s.executor.runner.Run(withSubmissionSource(instructionSequence{
		publishEventInstruction{event: SubmissionSucceeded{Raw: outcome.raw}},
	}, outcome.statement))
}

// QueueCompletion schedules caller-owned result handling, including during
// shutdown draining. Routing callbacks retain it in their causal delivery burst
// so accepted input records can precede the session events caused by delivery.
func (s *CommandSubmission) QueueCompletion(work func()) {
	s.session.queueCompletionWork(work)
}

// PublishCompletion publishes an already-settled result. The caller consumes
// duplicate/late results and closes its lifecycle before calling this method.
func (s *CommandSubmission) PublishCompletion(result runtime.CommandResult) {
	s.session.close()
	sequence := invocationCompletionSequence(s.outcome, result, s.shell.completionEvent(s.outcome))
	s.session.publishCompletion(sequence, len(result.Records))
}
