// Package runtime defines command contracts and a synchronous runner.
// The interpreter drives the runner on its serialized execution path.
package runtime

import (
	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/room"
)

// ParticipantReader is a read capability, not a session execution handle.
type ParticipantReader interface {
	Participants() []participant.View
}

// Context supplies capabilities during preparation on the serialized coordinator.
// This proof includes only the capability required by /who.
type Context struct {
	Participants ParticipantReader
}

// Command is stateless; Prepare creates independent state for one invocation.
type Command interface {
	Name() string
	Usage() string
	Description() string
	Prepare(Context) (Invocation, error)
}

// Invocation is driven exclusively by the coordinator: Init once, then Next
// until Done. Neither method performs I/O or publishes events.
type Invocation interface {
	Init()
	Next() Step
}

// Step contains transcript records from Next. Done means the invocation has
// completed; records returned with Done still need to be appended by the caller.
type Step struct {
	Records []room.Record
	Done    bool
}
