package record

import (
	"github.com/roomscript/coderoom/internal/agent"
	"github.com/roomscript/coderoom/internal/participant"
	roomstate "github.com/roomscript/coderoom/internal/room"
)

func NewAgent(alias string, msg agent.Message) Record {
	return roomstate.NewAgentRecord(participant.New(alias, "", participant.InitiativeManual).Alias, msg)
}
