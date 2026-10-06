package interpreter

import (
	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/session"
)

// SessionController is the session behavior consumed by Interpreter. It is an
// internal dependency port, not part of the front-end event or snapshot API.
type SessionController interface {
	Execute(session.Command) error
	AddObserver(session.Observer)
	PlanSharedSend(alias string) session.SharedSendPlan
	Participants() []participant.View
	Participant(alias string) (participant.View, bool)
	Shutdown()
}

var _ SessionController = (*session.Session)(nil)

type sessionObserver struct{ executor *interpreterExecutor }

func (o sessionObserver) OnEvent(event session.Event) {
	o.executor.recordSessionEvent(event)
}
