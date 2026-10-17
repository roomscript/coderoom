// Package runtime defines command contracts and a callback runner.
// The interpreter drives the runner on its serialized execution path.
package runtime

import (
	"github.com/roomscript/coderoom/internal/interpreter/api"
	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/promptlang"
	"github.com/roomscript/coderoom/internal/room"
	"github.com/roomscript/coderoom/internal/session"
	"github.com/roomscript/coderoom/internal/shell"
)

// ParticipantReader returns a fresh detached view on each call. This inspection
// capability does not establish lifecycle event-collection ordering.
// It is a read capability, not a session execution handle.
type ParticipantReader interface {
	Participants() []participant.View
}

// ShellLauncher owns asynchronous execution, cancellation and worker lifetime.
type ShellLauncher interface {
	Cwd() string
	Go(program string, complete func(shell.Result)) error
}

// SessionExecution is a single-use execution scope. Subscribe observes only this
// scope's terminal event, even when another execution has identical recipients.
// Subscribe is not inline and returns a non-nil, non-blocking unsubscribe function.
// Unsubscribe closes only this scope; already-running callbacks may finish.
// Execute admits one command without waiting for delivery. Admission errors return
// directly; accepted work emits DeliveryCompleted with Session's execution ID and
// routing outcome, or ExecutionFailed for a protocol/execution failure. Terminal
// events run in the owning dispatch burst before causal publication and may race
// launch/close. Handlers only collect facts or queue owner work; the interpreter
// drives execution.
type SessionExecution interface {
	Subscribe(handle func(ExecutionEvent)) (unsubscribe func())
	Execute(command session.Command) error
}

// SessionExecutionProvider supplies Session reads and fresh single-use execution
// scopes within one invocation. The interpreter owns the provider lifetime.
type SessionExecutionProvider interface {
	// CreateExecution returns a non-nil, independent scope. Closing one scope does
	// not affect siblings or the provider. Once its owner closes, existing and new
	// scopes reject admission. Creation may race owner closure; the returned scope
	// still rejects admission. It does not reserve participant availability.
	CreateExecution() SessionExecution
	// SubscribeParticipants atomically returns initial state and subscribes to
	// subsequent lifecycle facts in mutation order. It never replays facts from
	// before that snapshot. Callbacks may race its return, but are not inline;
	// unsubscribe prevents future callbacks without waiting for those already in
	// flight. It is non-blocking and safe from a callback.
	SubscribeParticipants(handle func(session.Event)) (initial session.ParticipantSnapshot, unsubscribe func())
	ParticipantByName(name string) (participant.View, bool)
	CreateParticipantSendPlan(alias participant.Alias) session.ParticipantSendPlan
}

// HistoryReader supplies detached completed output from the canonical room.
// Reads run on the interpreter lane at execution time. Selection can be extended
// when commands need earlier or multiple outputs; readiness is owned by staging.
type HistoryReader interface {
	LatestOutput(participant.Alias) (session.HandoffSource, bool)
}

// CompletedExecutionIDReader reports completed executions recorded in room state, including
// the historical attachment boundary. Runtime reads it only on the interpreter lane.
type CompletedExecutionIDReader interface {
	LastCompletedExecutionID(participant.Alias) participant.ExecutionID
}

// UserCommandRegistry stores and resolves shell-backed definitions on the
// interpreter lane. promptlang.Registry implements this capability.
type UserCommandRegistry interface {
	Define(promptlang.UserDefinition) error
	Resolve(promptlang.UserCommand) (promptlang.Located[promptlang.Shell], error)
}

// EventProcessingRequester queues interpreter attention for one invocation.
// Producers collect facts before requesting; processing never runs inline.
// Commands requiring event-driven resumption must require this capability.
type EventProcessingRequester interface {
	RequestEventProcessing()
}

// InvocationPublication publishes records and explicit semantic events while an
// invocation remains active. Call only from Go or HandlePendingEvents on the
// interpreter lane. Producers transfer ownership of the supplied records/events.
// Publication does not settle the invocation or close its execution scopes;
// the interpreter owns identity checks, source metadata and causal ordering.
type InvocationPublication interface {
	Publish(records []room.Record, events ...api.Event)
}

// Context supplies capabilities for preparation and Go on the serialized coordinator.
type Context struct {
	Publication         InvocationPublication
	EventProcessing     EventProcessingRequester
	UserCommands        UserCommandRegistry
	Participants        ParticipantReader
	Shell               ShellLauncher
	Session             SessionExecutionProvider
	History             HistoryReader
	CompletedExecutions CompletedExecutionIDReader
}

// Command is stateless; Prepare creates independent state for one invocation.
type Command interface {
	Name() string
	Usage() string
	Description() string
	// Statement identifies the accepted AST type; its value is not default input.
	Statement() promptlang.Statement
	Prepare(promptlang.ParsedStatement, Context) (Invocation, error)
}

// Invocation initiates work and reports completion through the supplied callback.
// Go returns launch failure; nil means completion will be reported separately.
// Implementations may complete immediately. The callback transfers ownership of
// its records to the caller; they must not be mutated afterward.
type Invocation interface {
	// Staging describes readiness needs before Go; nil means no waiting is needed.
	Staging() *Staging
	Go(complete func(CommandResult)) error
	// Close releases invocation-owned subscriptions; it is idempotent and runs
	// after the lifecycle stops collecting participant facts. Implementations must tolerate racing
	// callbacks. Cancellation of admitted delivery remains the host's job.
	Close()
}

// InvocationEventHandler is optional for invocations that coordinate work started
// as part of their own execution. A loop may send participant work, wait for its
// completion, run a condition command, and react to that result. Future blocks
// may likewise coordinate child commands within one enclosing invocation.
//
// Subcommand callbacks collect facts and request attention through
// Context.EventProcessing.RequestEventProcessing. They do not update the enclosing
// invocation's phase or start its next command themselves. The interpreter calls
// HandlePendingEvents after Go succeeds, on its serialized lane, so the invocation
// can consume those facts, update its state, and decide what to start next.
// Redundant calls may have no pending facts. Report final completion through the
// callback supplied to Go; the interpreter still owns settlement and shutdown.
type InvocationEventHandler interface {
	HandlePendingEvents()
}

// Staging describes the frozen participants an invocation needs ready before Go.
// Aliases retain instance identity; replacement participants cannot inherit waits.
// All listed aliases must be idle, startup-ready and have their last completed
// turn propagated into canonical interpreter state.
// The invocation owns this description; callers must treat it as read-only.
// WaitFor gates execution; ToInform describes frozen optional recipients, which
// also gate readiness (send notices or broadcast work). WaitFor does not imply
// delivery. Departure handling is
// command policy; the lifecycle tracks sticky departures.
type Staging struct {
	WaitFor  []participant.Alias
	ToInform []participant.Alias
	// DiscardInform removes an unavailable optional recipient from the command's
	// retained delivery plan. The lifecycle calls it before Go,
	// while removing that alias from its own waits. Nil means no update is needed.
	// Departures are sticky: rejoining cannot restore a discarded recipient.
	// An unavailable WaitFor alias fails staging; it never reaches this callback.
	DiscardInform func(alias participant.Alias)
}

// CommandResult contains final transcript records and the work's outcome.
// The interpreter owns enqueueing, ordered publication and duplicate handling.
type CommandResult struct {
	Records []room.Record
	Err     error
}
