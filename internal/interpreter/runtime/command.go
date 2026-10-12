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

// ParticipantReader returns a fresh detached view on each call. For subscribed
// collectors, lifecycle events represented by the view must be collected before
// this read returns, so initialization can preserve events over snapshot entries.
// It is a read capability, not a session execution handle.
type ParticipantReader interface {
	Participants() []participant.View
}

// ShellLauncher owns asynchronous execution, cancellation and worker lifetime.
type ShellLauncher interface {
	Cwd() string
	Go(program string, complete func(shell.Result)) error
}

// SessionExecution is a host admission and shared event capability, not raw Session.
// ParticipantByName resolves input names to current Session-owned identities.
// CreateParticipantSendPlan delegates recipient selection to Session.
// Subscribe receives all session events, including participant lifecycle facts
// before execution. The callback runs immediately and may run on a producer
// goroutine; it must be concurrency-safe. Subscribe itself does not invoke the
// callback inline. Subscribe always returns a non-nil unsubscribe function.
// Unsubscribe is safe from a callback and prevents further
// delivery without waiting for in-flight callbacks. Consumers own their queues.
// Execute must support admission from an invocation's worker while the host
// continues to serialize actual Session execution.
// Execute admits work without waiting for delivery; admission failures return an
// error, while delivery outcomes arrive as session.RoutingCompleted events.
// The host owns the serialized session execution lane and worker lifetime.
// A primary alias must have at most one admitted send awaiting its routing result;
// alias matching cannot distinguish concurrent requests to the same primary.
// This prototype has no production adapter yet.
type SessionExecution interface {
	Subscribe(handle func(session.Event)) (unsubscribe func())
	ParticipantByName(name string) (participant.View, bool)
	CreateParticipantSendPlan(alias participant.Alias) session.ParticipantSendPlan
	Execute(command session.Command) error
}

// Context supplies capabilities during preparation on the serialized coordinator.
type Context struct {
	Participants ParticipantReader
	Shell        ShellLauncher
	Session      SessionExecution
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
	// after the lifecycle stops its monitor. Implementations must tolerate racing
	// callbacks. Cancellation of admitted delivery remains the host's job.
	Close()
}

// Staging describes the frozen participants an invocation needs ready before Go.
// Aliases retain instance identity; replacement participants cannot inherit waits.
// All listed aliases use the shared readiness rule: idle and startup-ready.
// The invocation owns this description; callers must treat it as read-only.
// WaitFor gates execution; ToInform describes frozen contextual notice recipients, which
// also gate readiness. WaitFor does not imply delivery. Departure handling is
// command policy; the lifecycle tracks sticky departures. This draft is unwired.
type Staging struct {
	WaitFor  []participant.Alias
	ToInform []participant.Alias
	// DiscardInform removes an unavailable notice recipient from the command's
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
