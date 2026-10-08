package interpreter

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/roomscript/coderoom/internal/agent"
	"github.com/roomscript/coderoom/internal/agent/echo"
	roomconfig "github.com/roomscript/coderoom/internal/config"
	"github.com/roomscript/coderoom/internal/policy"
	"github.com/roomscript/coderoom/internal/session"
)

type startupNoticeAgent struct {
	*startupDeliveryAgent
	notices chan string
}

func (a *startupNoticeAgent) SendNotice(text string) (agent.StreamID, error) {
	a.notices <- text
	id, err := a.Client.SendNotice(text)
	if err != nil {
		return "", fmt.Errorf("send startup test notice: %w", err)
	}
	return id, nil
}

type startupListenerObserver struct {
	gate    startupIdleGate
	started chan string
}

func (o startupListenerObserver) OnEvent(event session.Event) {
	if status, ok := event.(session.ParticipantStatusChanged); ok && status.Alias == "ben" {
		o.gate.OnEvent(event)
	}
	if started, ok := event.(session.AgentReady); ok {
		o.started <- started.Alias
	}
}

func TestSubmit_sendNoticesWaitsForStartingListenerAndFreezesRecipients(t *testing.T) {
	sess, interp, events, observer, backends, release := newStartupNoticeInterpreter(t)
	inviteStartupNoticeParticipant(t, sess, observer, "ada")
	if err := sess.Execute(session.InviteCommand{Alias: "ben"}); err != nil {
		t.Fatal(err)
	}
	receiveSignal(t, observer.gate.entered, "listener startup window")
	mustSubmit(t, interp.Submit("@ada hello"))
	awaitStartupWindowEvent[SubmissionSucceeded](t, events)
	stage := awaitStartupWindowEvent[StateChanged](t, events).Snapshot.Stage
	if stage == nil || !slices.Equal(stage.NotReadyAliases, []string{"ben"}) {
		t.Fatalf("stage = %#v", stage)
	}
	assertNoStartupNoticeDelivery(t, backends["ada"])
	assertNoStartupNoticeDelivery(t, backends["ben"])

	inviteStartupNoticeParticipant(t, sess, observer, "later")
	assertNoStartupNoticeDelivery(t, backends["ada"])
	assertNoStartupNoticeDelivery(t, backends["later"])
	release()
	awaitStartupWindowEvent[StagedInputDispatched](t, events)
	assertStartupDelivery(t, backends["ada"].sent, true)
	select {
	case text := <-backends["ben"].notices:
		if text != "@ada: hello" {
			t.Fatalf("listener payload = %q", text)
		}
	default:
		t.Fatal("dispatch did not deliver listener notice")
	}
	// The expected payloads have been consumed; no extra delivery is allowed.
	for _, backend := range backends {
		assertNoStartupNoticeDelivery(t, backend)
	}
}

func newStartupNoticeInterpreter(t *testing.T) (*session.Session, *Interpreter, chan Event, startupListenerObserver, map[string]*startupNoticeAgent, func()) {
	t.Helper()
	observer := startupListenerObserver{
		gate:    startupIdleGate{entered: make(chan struct{}), release: make(chan struct{})},
		started: make(chan string, 3),
	}
	backends := make(map[string]*startupNoticeAgent)
	for _, alias := range []string{"ada", "ben", "later"} {
		backends[alias] = &startupNoticeAgent{
			startupDeliveryAgent: &startupDeliveryAgent{Client: echo.New(), sent: make(chan string, 4)},
			notices:              make(chan string, 4),
		}
	}
	sess := session.New(session.WithObserver(observer), session.WithAgentFactory(func(_ *session.Session, cfg roomconfig.ParticipantConfig, _ session.AgentBackend) agent.Agent {
		return backends[cfg.Alias]
	}))
	events := make(chan Event, 128)
	interp := New(context.Background(), sess, t.TempDir(), WithObserver(submitContractObserver{events: events}))
	t.Cleanup(interp.Close)
	var once sync.Once
	release := func() { once.Do(func() { close(observer.gate.release) }) }
	t.Cleanup(release)
	if err := sess.Execute(session.EnablePolicyCommand{Name: policy.SendNotices}); err != nil {
		t.Fatal(err)
	}
	return sess, interp, events, observer, backends, release
}

func inviteStartupNoticeParticipant(t *testing.T, sess *session.Session, observer startupListenerObserver, alias string) {
	t.Helper()
	if err := sess.Execute(session.InviteCommand{Alias: alias}); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-observer.started:
		if got != alias {
			t.Fatalf("started = %q, want %q", got, alias)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %s startup", alias)
	}
}

func assertNoStartupNoticeDelivery(t *testing.T, backend *startupNoticeAgent) {
	t.Helper()
	assertStartupDelivery(t, backend.sent, false)
	select {
	case text := <-backend.notices:
		t.Fatalf("unexpected listener notice: %q", text)
	default:
	}
}
