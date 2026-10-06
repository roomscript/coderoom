package session

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/roomscript/coderoom/internal/agent"
	roomconfig "github.com/roomscript/coderoom/internal/config"
	"github.com/roomscript/coderoom/internal/participant"
)

type keepaliveTestAgent struct {
	mu       sync.Mutex
	started  bool
	calls    int
	schedule time.Duration
	blockCh  chan struct{}
	readCh   chan agent.Message
	stopOnce sync.Once
	stopCh   chan struct{}
}

func (a *keepaliveTestAgent) Start() error { a.started = true; return nil }
func (a *keepaliveTestAgent) Send(string) (agent.StreamID, error) {
	return "", nil
}
func (a *keepaliveTestAgent) SendNotice(string) (agent.StreamID, error) {
	return "", nil
}
func (a *keepaliveTestAgent) Read() (agent.Message, error) {
	msg, ok := <-a.readCh
	if !ok {
		return agent.Message{}, errors.New("closed")
	}
	return msg, nil
}
func (a *keepaliveTestAgent) Interrupt() error { return nil }
func (a *keepaliveTestAgent) Stop() error {
	a.stopOnce.Do(func() {
		if a.stopCh != nil {
			close(a.stopCh)
		}
		close(a.readCh)
	})
	return nil
}
func (a *keepaliveTestAgent) KeepAliveSchedule() time.Duration {
	return a.schedule
}
func (a *keepaliveTestAgent) KeepAlive() error {
	a.mu.Lock()
	a.calls++
	a.mu.Unlock()
	if a.blockCh != nil {
		select {
		case <-a.blockCh:
		case <-a.stopCh:
			return nil
		}
	}
	select {
	case a.readCh <- agent.Message{
		Mode:    agent.ModeSingle,
		Content: agent.KeepAlive{},
	}:
	case <-a.stopCh:
		return nil
	}
	return nil
}

func (a *keepaliveTestAgent) CallCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.calls
}

type keepaliveObserver struct {
	ch chan Event
}

const keepaliveTestAlias = "ada"

func (o keepaliveObserver) OnEvent(e Event) {
	select {
	case o.ch <- e:
	default:
	}
}

func TestSession_keepaliveSweepTransitionsParticipant(t *testing.T) {
	s, events := newKeepaliveTestSession(5*time.Millisecond, func() agent.Agent {
		return newKeepaliveTestAgent(10*time.Millisecond, true)
	})
	t.Cleanup(s.Shutdown)
	mustInviteKeepaliveTestParticipant(t, s)
	waitForStatusEvent(t, events, participant.StatusIdle)

	ka := requireKeepaliveTestAgent(t, s)
	blockCh := ka.blockCh

	waitForStatusEvent(t, events, participant.StatusKeepalive)
	requireParticipantStatus(t, s, participant.StatusKeepalive)

	close(blockCh)
	waitForStatusEvent(t, events, participant.StatusIdle)
	requireParticipantStatus(t, s, participant.StatusIdle)
	if got := ka.CallCount(); got == 0 {
		t.Fatal("expected KeepAlive to be called at least once")
	}
}

func TestSession_keepaliveStopsWhenParentContextIsCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	agentFactory := func(_ *Session, _ roomconfig.ParticipantConfig, _ AgentBackend) agent.Agent {
		return &keepaliveTestAgent{
			schedule: 50 * time.Millisecond,
			readCh:   make(chan agent.Message, 1),
			stopCh:   make(chan struct{}),
		}
	}
	events := make(chan Event, 32)
	s := New(
		WithContext(ctx),
		func(s *Session) { s.keepaliveTick = 10 * time.Millisecond },
		WithAgentFactory(agentFactory),
		WithObserver(keepaliveObserver{ch: events}),
	)
	t.Cleanup(s.Shutdown)

	if err := s.Execute(InviteCommand{
		Alias: "ada",
	}); err != nil {
		t.Fatalf("InviteCommand: %v", err)
	}

	waitForStatusEvent(t, events, participant.StatusIdle)

	cancel()
	time.Sleep(120 * time.Millisecond)

	p, ok := s.readParticipantRuntime("ada")
	if !ok {
		t.Fatal("expected participant ada")
	}
	ka, ok := p.Agent.(*keepaliveTestAgent)
	if !ok {
		t.Fatalf("expected keepalive test agent, got %T", p.Agent)
	}
	if got := ka.CallCount(); got != 0 {
		t.Fatalf("expected cancelled parent context to stop keepalive, got %d calls", got)
	}
}

func TestSession_shutdownDuringKeepaliveReturnsPromptly(t *testing.T) {
	agentFactory := func(_ *Session, _ roomconfig.ParticipantConfig, _ AgentBackend) agent.Agent {
		return &keepaliveTestAgent{
			schedule: 10 * time.Millisecond,
			blockCh:  make(chan struct{}),
			readCh:   make(chan agent.Message, 1),
			stopCh:   make(chan struct{}),
		}
	}
	events := make(chan Event, 32)
	s := New(
		func(s *Session) { s.keepaliveTick = 5 * time.Millisecond },
		WithAgentFactory(agentFactory),
		WithObserver(keepaliveObserver{ch: events}),
	)

	if err := s.Execute(InviteCommand{
		Alias: "ada",
	}); err != nil {
		t.Fatalf("InviteCommand: %v", err)
	}

	waitForStatusEvent(t, events, participant.StatusIdle)
	waitForStatusEvent(t, events, participant.StatusKeepalive)

	done := make(chan struct{})
	go func() {
		s.Shutdown()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Shutdown() blocked during keepalive")
	}
}

func TestSession_sweepKeepaliveSkipsPreparingParticipant(t *testing.T) {
	s, events := newKeepaliveTestSession(time.Hour, func() agent.Agent {
		return newKeepaliveTestAgent(10*time.Millisecond, false)
	})
	t.Cleanup(s.Shutdown)
	mustInviteKeepaliveTestParticipant(t, s)

	waitForStatusEvent(t, events, participant.StatusIdle)

	if err := s.prepareParticipantForWork(keepaliveTestAlias); err != nil {
		t.Fatalf("prepareParticipantForWork: %v", err)
	}

	s.sweepKeepalive()

	p, ok := s.Participant(keepaliveTestAlias)
	if !ok {
		t.Fatalf("expected participant %s", keepaliveTestAlias)
	}
	if p.Status != participant.StatusPreparing {
		t.Fatalf("expected preparing status, got %q", p.Status)
	}
	if got := requireKeepaliveTestAgent(t, s).CallCount(); got != 0 {
		t.Fatalf("expected no keepalive call while preparing, got %d", got)
	}
}

func TestSession_sweepKeepaliveSkipsMissingRuntime(t *testing.T) {
	s, events := newKeepaliveTestSession(time.Hour, func() agent.Agent {
		return newKeepaliveTestAgent(10*time.Millisecond, false)
	})
	t.Cleanup(s.Shutdown)

	mustInviteKeepaliveTestParticipant(t, s)
	waitForStatusEvent(t, events, participant.StatusIdle)

	p, ok := s.Participant(keepaliveTestAlias)
	if !ok {
		t.Fatalf("expected participant %s", keepaliveTestAlias)
	}
	ka := requireKeepaliveTestAgent(t, s)

	s.cancelAgentContext(keepaliveTestAlias)
	s.now = func() time.Time { return p.Since.Add(time.Hour) }
	s.sweepKeepalive()

	if got := ka.CallCount(); got != 0 {
		t.Fatalf("expected no keepalive call without runtime, got %d", got)
	}
}

func newKeepaliveTestAgent(schedule time.Duration, withBlockCh bool) *keepaliveTestAgent {
	a := &keepaliveTestAgent{
		schedule: schedule,
		readCh:   make(chan agent.Message, 1),
		stopCh:   make(chan struct{}),
	}
	if withBlockCh {
		a.blockCh = make(chan struct{})
	}
	return a
}

func newKeepaliveTestSession(tick time.Duration, factory func() agent.Agent) (*Session, chan Event) {
	events := make(chan Event, 32)
	s := New(
		func(s *Session) { s.keepaliveTick = tick },
		WithAgentFactory(func(_ *Session, _ roomconfig.ParticipantConfig, _ AgentBackend) agent.Agent { return factory() }),
		WithObserver(keepaliveObserver{ch: events}),
	)
	return s, events
}

func mustInviteKeepaliveTestParticipant(t *testing.T, s *Session) {
	t.Helper()
	if err := s.Execute(InviteCommand{
		Alias: keepaliveTestAlias,
	}); err != nil {
		t.Fatalf("InviteCommand: %v", err)
	}
}

func requireKeepaliveTestAgent(t *testing.T, s *Session) *keepaliveTestAgent {
	t.Helper()
	p, ok := s.readParticipantRuntime(keepaliveTestAlias)
	if !ok {
		t.Fatalf("expected participant %s", keepaliveTestAlias)
	}
	ka, ok := p.Agent.(*keepaliveTestAgent)
	if !ok {
		t.Fatalf("expected keepalive test agent, got %T", p.Agent)
	}
	return ka
}

func requireParticipantStatus(t *testing.T, s *Session, want participant.Status) {
	t.Helper()
	p, ok := s.Participant(keepaliveTestAlias)
	if !ok {
		t.Fatalf("expected participant %s", keepaliveTestAlias)
	}
	if p.Status != want {
		t.Fatalf("expected status %q, got %q", want, p.Status)
	}
}

func waitForStatusEvent(t *testing.T, events <-chan Event, to participant.Status) {
	t.Helper()
	deadline := time.After(time.Second)
	for {
		select {
		case ev := <-events:
			status, ok := ev.(ParticipantStatusChanged)
			if ok && status.Alias == keepaliveTestAlias && status.To == to {
				return
			}
		case <-deadline:
			t.Fatalf("timed out waiting for %q to reach %q", keepaliveTestAlias, to)
		}
	}
}
