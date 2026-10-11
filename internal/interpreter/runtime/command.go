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

// ParticipantReader is a read capability, not a session execution handle.
type ParticipantReader interface {
	Participants() []participant.View
}

// ShellLauncher owns asynchronous execution, cancellation and worker lifetime.
type ShellLauncher interface {
	Cwd() string
	Go(program string, complete func(shell.Result)) error
}

// SessionExecution is a host capability scoped to one invocation, not raw Session.
// CreateParticipantSendPlan delegates recipient selection to Session.
// Subscribe registers before Execute. The host queues request-correlated events
// on its serialized path, never invoking the handler inline. Unsubscribe also
// suppresses events already queued for this subscription.
// Execute admits work without waiting for delivery; admission failures return an
// error, while delivery outcomes arrive as session.RoutingCompleted events.
// The host owns the serialized session execution lane and worker lifetime.
// This prototype has no production adapter yet.
type SessionExecution interface {
	Subscribe(handle func(session.Event)) (unsubscribe func())
	CreateParticipantSendPlan(alias string) session.ParticipantSendPlan
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
	// on the host serialized path. Worker cancellation remains the host's job.
	Close()
}

// Staging describes the frozen participants an invocation needs ready before Go.
// All listed aliases use the shared readiness rule: idle and startup-ready.
// The invocation owns this description; callers must treat it as read-only.
// WaitFor gates execution; ToInform describes frozen contextual notice recipients, which
// also gate readiness. WaitFor does not imply delivery. Departure handling is
// command policy; the host tracks sticky departures. This draft is unwired.
type Staging struct {
	WaitFor  []string
	ToInform []string
}

// CommandResult contains final transcript records and the work's outcome.
// The interpreter owns enqueueing, ordered publication and duplicate handling.
type CommandResult struct {
	Records []room.Record
	Err     error
}
