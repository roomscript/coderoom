package record

import (
	"github.com/roomscript/coderoom/internal/agent"
	roomstate "github.com/roomscript/coderoom/internal/room"
)

func NewAgent(alias string, msg agent.Message) Record {
	return roomstate.NewAgentRecord(alias, msg)
}
