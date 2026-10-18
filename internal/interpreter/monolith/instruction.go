package monolith

import "github.com/roomscript/coderoom/internal/room"

// Instructions describe canonical records, semantic events and snapshots on the lane.
type instruction interface{ instruction() }
type publishSnapshotInstruction struct{}
type requestSnapshotInstruction struct{}
type appendRecordInstruction struct{ record room.Record }
type publishEventInstruction struct{ event Event }

func (publishSnapshotInstruction) instruction() {}
func (requestSnapshotInstruction) instruction() {}
func (appendRecordInstruction) instruction()    {}
func (publishEventInstruction) instruction()    {}

type instructionSequence []instruction
