// Package runtime defines command contracts and a callback runner.
// The interpreter drives the runner on its serialized execution path.
package runtime

import (
	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/room"
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

// Context supplies capabilities during preparation on the serialized coordinator.
type Context struct {
	Participants ParticipantReader
	Shell        ShellLauncher
}

// Command is stateless; Prepare creates independent state for one invocation.
type Command interface {
	Name() string
	Usage() string
	Description() string
	Prepare(Context) (Invocation, error)
}

// Invocation initiates work and reports completion through the supplied callback.
// Go returns launch failure; nil means completion will be reported separately.
// Implementations may complete immediately. The callback transfers ownership of
// its records to the caller; they must not be mutated afterward.
type Invocation interface {
	Go(complete func(Completion)) error
}

// Completion contains final transcript records and the work's outcome.
// The interpreter owns enqueueing, ordered publication and duplicate handling.
type Completion struct {
	Records []room.Record
	Err     error
}
