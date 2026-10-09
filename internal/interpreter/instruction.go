package interpreter

import (
	"github.com/roomscript/coderoom/internal/room"
	"github.com/roomscript/coderoom/internal/session"
)

// Instructions describe work for the serialized runner. Preparation reads return
// immediately; shell-launch instructions start asynchronous work.
type instruction interface{ instruction() }

type executeSessionInstruction struct {
	target  workflowRef
	request sessionRequest
	// recordsOnSuccess are applied on acceptance (including partial delivery)
	// before causal events; non-routing commands require a nil execution error.
	recordsOnSuccess []room.Record
}

type startShellInstruction struct {
	target  workflowRef
	request shellRequest
}

type executeCommandInstruction struct {
	command session.Command
	outcome submissionOutcome
}

type startUserShellInstruction struct {
	raw     string
	command string
	program string
}

type readRosterInstruction struct{ raw string }
type prepareSendInstruction struct {
	target workflowRef
	alias  string
}
type planBroadcastInstruction struct{ target workflowRef }
type readParticipantStateInstruction struct{ target workflowRef }
type readHandoffSourceInstruction struct {
	target workflowRef
	alias  string
}
type publishSnapshotInstruction struct{}
type requestSnapshotInstruction struct{}
type requestCloseInstruction struct{}
type shutdownSessionInstruction struct{}

type appendRecordInstruction struct{ record room.Record }
type publishEventInstruction struct{ event Event }

func (executeSessionInstruction) instruction()       {}
func (startShellInstruction) instruction()           {}
func (executeCommandInstruction) instruction()       {}
func (startUserShellInstruction) instruction()       {}
func (readRosterInstruction) instruction()           {}
func (prepareSendInstruction) instruction()          {}
func (planBroadcastInstruction) instruction()        {}
func (readParticipantStateInstruction) instruction() {}
func (readHandoffSourceInstruction) instruction()    {}
func (publishSnapshotInstruction) instruction()      {}
func (requestSnapshotInstruction) instruction()      {}
func (requestCloseInstruction) instruction()         {}
func (shutdownSessionInstruction) instruction()      {}
func (appendRecordInstruction) instruction()         {}
func (publishEventInstruction) instruction()         {}

type instructionSequence []instruction

func (s *instructionSequence) append(next instructionSequence) {
	*s = append(*s, next...)
}
