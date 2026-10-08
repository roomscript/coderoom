//go:build integration

package session_test

import (
	"os"
	"testing"
	"time"

	"github.com/roomscript/coderoom/internal/agent"
	"github.com/roomscript/coderoom/internal/agent/codex"
	roomconfig "github.com/roomscript/coderoom/internal/config"
	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/session"
)

type eventBuf struct {
	ch  <-chan session.Event
	buf []session.Event
}

func newEventBuf(ch <-chan session.Event) *eventBuf {
	return &eventBuf{ch: ch}
}

func (b *eventBuf) waitFor(t *testing.T, timeout time.Duration, pred func(session.Event) bool) session.Event {
	t.Helper()
	deadline := time.After(timeout)
	for {
		for i, ev := range b.buf {
			if pred(ev) {
				b.buf = append(b.buf[:i], b.buf[i+1:]...)
				return ev
			}
		}
		select {
		case ev, ok := <-b.ch:
			if !ok {
				t.Fatal("events channel closed while waiting for event")
				return nil
			}
			if pred(ev) {
				return ev
			}
			b.buf = append(b.buf, ev)
		case <-deadline:
			t.Fatalf("timed out after %s waiting for event", timeout)
			return nil
		}
	}
}

// chanObserver forwards session events to a buffered channel.
type chanObserver struct {
	ch chan session.Event
}

func (o chanObserver) OnEvent(e session.Event) {
	select {
	case o.ch <- e:
	default:
	}
}

// drainUntilType reads from ch until an event of the expected type arrives or
// the timeout elapses. It discards other events so callers don't need to drain
// intermediate deltas before waiting for a lifecycle event.
func drainUntilType[T session.Event](t *testing.T, ch <-chan session.Event, timeout time.Duration) T {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				t.Fatalf("events channel closed while waiting for %T", *new(T))
				var zero T
				return zero
			}
			if typed, ok := ev.(T); ok {
				return typed
			}
		case <-deadline:
			t.Fatalf("timed out after %s waiting for %T event", timeout, *new(T))
			var zero T
			return zero
		}
	}
}

// drainUntilIdle reads events until the participant returns to idle.
func drainUntilIdle(t *testing.T, ch <-chan session.Event, timeout time.Duration) {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				t.Fatal("events channel closed while waiting for agent idle")
				return
			}
			status, ok := ev.(session.ParticipantStatusChanged)
			if ok && status.From == participant.StatusWorking && status.To == participant.StatusIdle {
				return
			}
		case <-deadline:
			t.Fatalf("timed out after %s waiting for agent idle", timeout)
		}
	}
}

func newSessionWithCodexAgents(t *testing.T, aliases ...string) (*session.Session, chan session.Event) {
	t.Helper()
	cwd, _ := os.Getwd()
	events := make(chan session.Event, 1024)
	agents := make(map[string]agent.Agent, len(aliases))
	for _, alias := range aliases {
		agents[alias] = codex.New(cwd)
	}
	s := session.New(
		session.WithObserver(chanObserver{ch: events}),
		session.WithAgentFactory(func(_ *session.Session, cfg roomconfig.ParticipantConfig, _ session.AgentBackend) agent.Agent {
			return agents[cfg.Alias]
		}),
	)
	t.Cleanup(s.Shutdown)
	t.Cleanup(func() {
		for _, alias := range aliases {
			_ = s.Execute(session.RemoveCommand{Alias: alias})
		}
	})
	for _, alias := range aliases {
		inviteAndWaitStarted(t, s, events, alias)
	}
	return s, events
}

func inviteAndWaitStarted(t *testing.T, s *session.Session, events <-chan session.Event, alias string) {
	t.Helper()
	if err := s.Execute(session.InviteCommand{
		Alias: alias,
	}); err != nil {
		t.Fatalf("invite %s: %v", alias, err)
	}
	drainUntilType[session.AgentReady](t, events, 10*time.Second)
}

func waitForFirstStreamOutput(t *testing.T, b *eventBuf, alias string, timeout time.Duration) {
	t.Helper()
	b.waitFor(t, timeout, func(ev session.Event) bool {
		msg, ok := ev.(session.AgentMessage)
		if !ok || msg.Alias != alias {
			return false
		}
		if _, ok := msg.Msg.Content.(agent.Output); ok && msg.Msg.Mode == agent.ModeFlush {
			t.Fatalf("%s: turn completed before streaming output observed; cannot assert working state", alias)
		}
		if _, ok := msg.Msg.Content.(agent.Output); ok && msg.Msg.Mode == agent.ModeStream {
			return true
		}
		return false
	})
}

func waitForFirstOutputFlush(t *testing.T, b *eventBuf, alias string, timeout time.Duration) {
	t.Helper()
	b.waitFor(t, timeout, func(ev session.Event) bool {
		msg, ok := ev.(session.AgentMessage)
		if !ok || msg.Alias != alias {
			return false
		}
		if _, ok := msg.Msg.Content.(agent.Output); ok && msg.Msg.Mode == agent.ModeFlush {
			return true
		}
		return false
	})
}

func waitForFirstStreamOutputThenCancel(t *testing.T, b *eventBuf, s *session.Session, alias string, timeout time.Duration) {
	t.Helper()
	waitForFirstStreamOutput(t, b, alias, timeout)
	if err := s.Execute(session.CancelCommand{Alias: alias}); err != nil {
		t.Fatalf("cancel: %v", err)
	}
}
