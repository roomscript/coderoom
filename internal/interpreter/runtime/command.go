// Package runtime defines command contracts and a callback runner.
// The interpreter drives the runner on its serialized execution path.
package runtime

import (
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

// SessionExecution supplies invocation-scoped terminal events and participant
// capabilities, not raw Session. Subscribe observes only this instance's execution:
// other invocations and legacy dispatch must never deliver terminal events here,
// even with identical routing kinds and recipients. Subscribe does not call inline
// and returns a non-nil, non-blocking unsubscribe function, safe from a callback.
// Unsubscribe prevents future delivery; already-running callbacks may finish.
// Execute admits one command without waiting for delivery. Admission errors return
// directly. Accepted execution emits DeliveryCompleted with the actual Session
// outcome, or ExecutionFailed for a missing/invalid outcome or protocol failure.
// Terminal events run within the owning dispatch burst before projection and may
// race launch/close; handlers must only queue owner work. The host serializes
// Session execution, supports worker admission and owns admitted work lifetime.
// Commands supply only the actual command. Session owns availability and routing.
type SessionExecution interface {
	Subscribe(handle func(ExecutionEvent)) (unsubscribe func())
	// SubscribeParticipants atomically returns initial state and subscribes to
	// subsequent lifecycle facts in mutation order. It never replays facts from
	// before that snapshot. Callbacks may race its return, but are not inline;
	// unsubscribe is non-blocking, safe from a callback, and prevents future
	// callbacks without waiting for those already in flight.
	SubscribeParticipants(handle func(session.Event)) (initial session.ParticipantSnapshot, unsubscribe func())
	// ParticipantByName resolves input names to current Session-owned instances.
	ParticipantByName(name string) (participant.View, bool)
	// CreateParticipantSendPlan delegates policy-aware recipient selection to Session.
	CreateParticipantSendPlan(alias participant.Alias) session.ParticipantSendPlan
	Execute(command session.Command) error
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

// Context supplies capabilities for preparation and Go on the serialized coordinator.
type Context struct {
	Participants        ParticipantReader
	Shell               ShellLauncher
	Session             SessionExecution
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
