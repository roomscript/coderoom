package interpreter

import (
	"testing"

	"github.com/trigosec/coderoom/internal/participant"
	"github.com/trigosec/coderoom/internal/session"
)

type startupSession struct{ *submitContractSession }

func (s startupSession) AddObserver(observer session.Observer) {
	s.submitContractSession.AddObserver(observer)
	observer.OnEvent(session.AgentStarted{Alias: "ada"})
}

func TestWithObserver_initialStatePrecedesStartupCallbacks(t *testing.T) {
	sess := startupSession{newSubmitContractSession()}
	sess.roster = []participant.View{{Alias: "ada", Status: participant.StatusIdle, StartupReady: true}}
	events := make(chan Event, 8)
	interp := New(t.Context(), sess, t.TempDir(), WithObserver(submitContractObserver{events: events}))
	t.Cleanup(interp.Close)
	initial := receiveSubmitEvent[StateChanged](t, events)
	if len(initial.Snapshot.Room.Members) != 0 || len(initial.Snapshot.Participants) != 1 || initial.Snapshot.Participants[0].Alias != "ada" {
		t.Fatalf("initial snapshot = %#v", initial.Snapshot)
	}
	changed := receiveSubmitEvent[StateChanged](t, events)
	if len(changed.Snapshot.Room.Members) != 1 || changed.Snapshot.Room.Members[0] != "ada" {
		t.Fatalf("startup members = %v", changed.Snapshot.Room.Members)
	}
	assertNoSubmitEvent(t, events)
}

func TestWithObserver_deliversInitialStateToEveryConfiguredObserver(t *testing.T) {
	first, second := make(chan Event, 4), make(chan Event, 4)
	interp := New(t.Context(), newSubmitContractSession(), t.TempDir(),
		WithObserver(submitContractObserver{events: first}), WithObserver(nil),
		WithObserver(submitContractObserver{events: second}))
	t.Cleanup(interp.Close)
	receiveSubmitEvent[StateChanged](t, first)
	receiveSubmitEvent[StateChanged](t, second)
	mustSubmit(t, interp.Submit("/undefined"))
	receiveSubmitEvent[UnknownCommand](t, first)
	receiveSubmitEvent[UnknownCommand](t, second)
}
