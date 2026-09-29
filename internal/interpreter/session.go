package interpreter

import (
	"github.com/trigosec/coderoom/internal/participant"
	"github.com/trigosec/coderoom/internal/session"
)

// SessionController is the session behavior consumed by Interpreter. It is an
// internal dependency port, not part of the front-end event or snapshot API.
type SessionController interface {
	Execute(session.Command) error
	AddObserver(session.Observer)
	PlanSharedSend(alias string) session.SharedSendPlan
	Roster() []participant.View
	Participant(alias string) (participant.Participant, bool)
	RoutableParticipants() []participant.Participant
	BarrierParticipants() []participant.Participant
	Shutdown()
}

var _ SessionController = (*session.Session)(nil)

type sessionObserver struct{ executor *interpreterExecutor }

func (o sessionObserver) OnEvent(event session.Event) {
	o.executor.recordSessionEvent(event)
}
