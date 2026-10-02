package record

import (
	"github.com/trigosec/coderoom/internal/agent"
	roomstate "github.com/trigosec/coderoom/internal/room"
)

func NewAgent(alias string, msg agent.Message) Record {
	return roomstate.NewAgentRecord(alias, msg)
}
