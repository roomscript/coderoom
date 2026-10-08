package session

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/roomscript/coderoom/internal/agent/echo"
	"github.com/roomscript/coderoom/internal/participant"
)

type shutdownStartupAgent struct {
	*echo.Client
	startGate chan struct{}
	stopped   chan struct{}
	stopOnce  sync.Once
	stopCalls atomic.Int32
}

func (a *shutdownStartupAgent) Start() error {
	<-a.startGate
	if err := a.Client.Start(); err != nil {
		return fmt.Errorf("start test agent: %w", err)
	}
	return nil
}

func (a *shutdownStartupAgent) Stop() error {
	a.stopCalls.Add(1)
	err := a.Client.Stop()
	a.stopOnce.Do(func() { close(a.stopped) })
	if err != nil {
		return fmt.Errorf("stop test agent: %w", err)
	}
	return nil
}

func TestStartup_shutdownRejectsLateAttachment(t *testing.T) {
	for _, tc := range []struct {
		name     string
		shutdown func(*Session)
	}{
		{"lifetime cancelled", (*Session).stopBackgroundLoops},
		{"runtimes removed", (*Session).cancelAllAgentContexts},
		{"shutdown completed", (*Session).Shutdown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := New()
			t.Cleanup(s.Shutdown)
			p := participant.New("ada", "builder", participant.InitiativeManual)
			p.BeginStartup(s.now())
			if err := s.addParticipant(p); err != nil {
				t.Fatal(err)
			}
			s.CreateAgentRuntime("ada")
			a := &shutdownStartupAgent{
				Client: echo.New(), startGate: make(chan struct{}), stopped: make(chan struct{}),
			}
			startInvitedAgent("ada", a, s)
			tc.shutdown(s)
			close(a.startGate)
			select {
			case <-a.stopped:
			case <-time.After(2 * time.Second):
				t.Fatal("late startup was not stopped")
			}
			s.Shutdown()
			if got := a.stopCalls.Load(); got != 1 {
				t.Fatalf("agent stopped %d times, want 1", got)
			}
			if got := s.snapshotAgentsToStop(); len(got) != 0 {
				t.Fatalf("late startup left %d bound agents", len(got))
			}
			view, ok := s.Participant("ada")
			if !ok || view.Status != participant.StatusStarting || view.StartupReady {
				t.Fatalf("failed attachment changed participant: %#v, %v", view, ok)
			}
		})
	}
}
