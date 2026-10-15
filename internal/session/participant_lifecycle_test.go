package session_test

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/roomscript/coderoom/internal/agent"
	"github.com/roomscript/coderoom/internal/interpreter/runtime"
	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/session"
)

// Only the real participant boundary is exercised here. The probe checks native
// readiness at launch; it neither admits Session work nor consumes routing events.
type participantLifecycleHost struct{ *session.Session }

func (*participantLifecycleHost) Subscribe(func(session.Event)) func() { return func() {} }

type readinessProbe struct {
	session  *session.Session
	staging  *runtime.Staging
	started  chan struct{}
	launches atomic.Int32
}

func (p *readinessProbe) Staging() *runtime.Staging { return p.staging }
func (*readinessProbe) Close()                      {}
func (p *readinessProbe) Go(func(runtime.CommandResult)) error {
	p.launches.Add(1)
	view, ok := p.session.Participant(p.staging.WaitFor[0])
	if !ok || !view.IsReadyForWork() {
		return errors.New("launch rejected by native readiness")
	}
	p.started <- struct{}{}
	return nil
}

func newDelayedReadySession(t *testing.T) (*session.Session, *mockAgent, participant.Alias, func()) {
	t.Helper()
	blocker := &blockedReadyObserver{entered: make(chan participant.Alias, 1), release: make(chan struct{})}
	a := newMockAgent()
	s := newSession(t, session.WithObserver(blocker), fixedFactory(a))
	var once sync.Once
	release := func() { once.Do(func() { close(blocker.release) }) }
	t.Cleanup(release)
	invite(t, s, "ada")
	select {
	case alias := <-blocker.entered:
		return s, a, alias, release
	case <-time.After(time.Second):
		t.Fatal("ordinary ready notification did not reach observer")
		return nil, nil, participant.Alias{}, release
	}
}

func TestParticipantLifecycle_busySnapshotIgnoresDelayedOrdinaryReady(t *testing.T) {
	s, a, alias, release := newDelayedReadySession(t)
	if err := s.Execute(session.SendToParticipantOutsideRoomCommand{Alias: alias, Text: "first turn"}); err != nil {
		t.Fatal(err)
	}
	probe := &readinessProbe{session: s, staging: &runtime.Staging{WaitFor: []participant.Alias{alias}}, started: make(chan struct{}, 1)}
	host := &participantLifecycleHost{Session: s}
	lifecycle := runtime.NewInvocationLifecycle(probe, runtime.Context{Session: host})
	advances := make(chan struct{}, 16)
	lifecycle.RequestAdvance = func() { advances <- struct{}{} }
	defer lifecycle.Close(errors.New("cleanup"))
	if _, err := lifecycle.Start(func(runtime.CommandResult) { t.Error("unexpected completion") }); err != nil {
		t.Fatalf("busy snapshot caused premature launch failure: %v", err)
	}
	if probe.launches.Load() != 0 {
		t.Fatal("busy snapshot launched work")
	}
	release() // Old ordinary AgentReady must not advance the ordered subscription.
	a.ch <- agent.Message{StreamID: mockTurnAnchor, Mode: agent.ModeFlush, Content: agent.Output{}}
	select {
	case <-advances:
		lifecycle.Advance()
	case <-time.After(time.Second):
		t.Fatal("later native idle transition did not request advancement")
	}
	waitParticipantLifecycleSignal(t, probe.started)
	lifecycle.Close(errors.New("cleanup"))
	if probe.launches.Load() != 1 {
		t.Fatal("waiting work did not launch exactly once")
	}
}

type heldParticipantSnapshot struct {
	participantLifecycleHost
	entered       chan struct{}
	release       chan struct{}
	idleCollected chan struct{}
}

func (h *heldParticipantSnapshot) SubscribeParticipants(handle func(session.Event)) ([]participant.View, func()) {
	initial, unsubscribe := h.Session.SubscribeParticipants(func(event session.Event) {
		handle(event)
		if status, ok := event.(session.ParticipantStatusChanged); ok && status.To == participant.StatusIdle {
			h.idleCollected <- struct{}{}
		}
	})
	close(h.entered)
	<-h.release
	return initial, unsubscribe
}

func waitParticipantLifecycleSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(time.Second):
		t.Fatal("participant lifecycle did not reach synchronization point")
	}
}

func TestParticipantLifecycle_idleFactDuringInitialReturnWins(t *testing.T) {
	a, observer := newMockAgent(), newTestObserver()
	s := newSession(t, fixedFactory(a), session.WithObserver(observer))
	invite(t, s, "ada")
	alias := mustReceive[session.AgentReady](t, observer.ch).Alias
	if err := s.Execute(session.SendToParticipantOutsideRoomCommand{Alias: alias, Text: "first turn"}); err != nil {
		t.Fatal(err)
	}
	host := &heldParticipantSnapshot{
		participantLifecycleHost: participantLifecycleHost{Session: s},
		entered:                  make(chan struct{}), release: make(chan struct{}), idleCollected: make(chan struct{}, 1),
	}
	probe := &readinessProbe{session: s, staging: &runtime.Staging{WaitFor: []participant.Alias{alias}}, started: make(chan struct{}, 1)}
	lifecycle := runtime.NewInvocationLifecycle(probe, runtime.Context{Session: host})
	var once sync.Once
	unblock := func() { once.Do(func() { close(host.release) }) }
	defer func() { unblock(); lifecycle.Close(errors.New("cleanup")) }()
	started := make(chan error, 1)
	go func() {
		_, err := lifecycle.Start(func(runtime.CommandResult) { t.Error("unexpected completion") })
		started <- err
	}()
	waitParticipantLifecycleSignal(t, host.entered)
	a.ch <- agent.Message{StreamID: mockTurnAnchor, Mode: agent.ModeFlush, Content: agent.Output{}}
	waitParticipantLifecycleSignal(t, host.idleCollected)
	unblock() // Return the earlier busy snapshot after the newer idle fact.
	if err := <-started; err != nil {
		t.Fatal(err)
	}
	waitParticipantLifecycleSignal(t, probe.started)
	if probe.launches.Load() != 1 {
		t.Fatal("post-snapshot readiness did not launch exactly once")
	}
}
