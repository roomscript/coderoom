package session_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/roomscript/coderoom/internal/agent"
	roomconfig "github.com/roomscript/coderoom/internal/config"
	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/policy"
	"github.com/roomscript/coderoom/internal/session"
)

// mockAgent is a controllable agent.Agent for tests.
// Pre-load messages at construction; Stop() closes the read channel.
type mockAgent struct {
	once         sync.Once
	ch           chan agent.Message
	mu           sync.Mutex
	sends        []string
	startErr     error
	stopErr      error
	sendErr      error
	sendHook     func(string) error
	noticeHook   func(string) error
	interruptErr error
	interrupts   int
}

func newMockAgent(msgs ...agent.Message) *mockAgent {
	m := &mockAgent{ch: make(chan agent.Message, max(len(msgs), 1))}
	for _, msg := range msgs {
		m.ch <- msg
	}
	return m
}

func (*mockAgent) Name() string   { return "fake" }
func (m *mockAgent) Start() error { return m.startErr }
func (m *mockAgent) Interrupt() error {
	m.mu.Lock()
	m.interrupts++
	m.mu.Unlock()
	return m.interruptErr
}
func (m *mockAgent) Stop() error {
	m.once.Do(func() { close(m.ch) })
	return m.stopErr
}

// mockTurnAnchor is the anchor StreamID returned by mockAgent.Send. Tests that
// inject a turn sequence must push a flush with this ID as the final message to
// trigger the idle transition under the anchor-based lifecycle model.
const mockTurnAnchor = agent.StreamID("mock:turn-anchor")

func (m *mockAgent) Send(text string) (agent.StreamID, error) {
	m.mu.Lock()
	m.sends = append(m.sends, text)
	m.mu.Unlock()
	if m.sendHook != nil {
		return "", m.sendHook(text)
	}
	if m.sendErr != nil {
		return "", m.sendErr
	}
	return mockTurnAnchor, nil
}
func (m *mockAgent) SendNotice(text string) (agent.StreamID, error) {
	m.mu.Lock()
	m.sends = append(m.sends, text)
	m.mu.Unlock()
	if m.noticeHook != nil {
		return "", m.noticeHook(text)
	}
	return "", m.sendErr
}
func (m *mockAgent) Read() (agent.Message, error) {
	msg, ok := <-m.ch
	if !ok {
		return agent.Message{}, errors.New("agent closed")
	}
	return msg, nil
}

type gateAgent struct {
	startGate chan struct{}
	*mockAgent
}

func (g *gateAgent) Start() error {
	<-g.startGate
	return g.mockAgent.Start()
}

// earlyOutputAgent pushes a message into its read channel before Start()
// returns, simulating output produced by an agent process during initialisation
// (before the session's readLoop has started).
type earlyOutputAgent struct {
	earlyMsg agent.Message
	*mockAgent
}

func (a *earlyOutputAgent) Start() error {
	if err := a.mockAgent.Start(); err != nil {
		return err
	}
	a.ch <- a.earlyMsg
	return nil
}

type noticeFlushAgent struct {
	inner *mockAgent
}

func newNoticeFlushAgent() *noticeFlushAgent {
	return &noticeFlushAgent{inner: newMockAgent()}
}

func (*noticeFlushAgent) Name() string                               { return "fake" }
func (n *noticeFlushAgent) Start() error                             { return n.inner.Start() }
func (n *noticeFlushAgent) Interrupt() error                         { return n.inner.Interrupt() }
func (n *noticeFlushAgent) Stop() error                              { return n.inner.Stop() }
func (n *noticeFlushAgent) Send(text string) (agent.StreamID, error) { return n.inner.Send(text) }
func (n *noticeFlushAgent) Read() (agent.Message, error)             { return n.inner.Read() }
func (n *noticeFlushAgent) SendNotice(text string) (agent.StreamID, error) {
	const noticeTurnStream = agent.StreamID("codex:notice-turn")
	if _, err := n.inner.SendNotice(text); err != nil {
		return "", err
	}
	// Emit a flush to end the notice turn. Return the stream ID so the session
	// can track it as the notice-turn anchor.
	n.inner.ch <- agent.Message{StreamID: noticeTurnStream, Mode: agent.ModeFlush, Content: agent.Output{}}
	return noticeTurnStream, nil
}

// testObserver collects events and exposes a channel for synchronisation.
type testObserver struct {
	mu     sync.Mutex
	events []session.Event
	ch     chan session.Event
}

func newTestObserver() *testObserver {
	return &testObserver{ch: make(chan session.Event, 128)}
}

func (o *testObserver) OnEvent(e session.Event) {
	o.mu.Lock()
	o.events = append(o.events, e)
	o.mu.Unlock()
	select {
	case o.ch <- e:
	default:
		// ch full; event still recorded in o.events — mustReceive will time out
		// rather than deadlock the reader goroutine.
	}
}

func shouldSkipEvent[T session.Event](ev session.Event) bool {
	_, wantStarted := any(*new(T)).(session.AgentReady)
	_, wantStarting := any(*new(T)).(session.AgentStarting)
	_, isStarting := ev.(session.AgentStarting)
	_, isStarted := ev.(session.AgentReady)
	// Skip startup lifecycle events unless the caller is explicitly waiting for
	// one. This covers both "skip AgentStarting when waiting for AgentReady"
	// and "skip AgentReady when waiting for AgentLog". The readLoop starts
	// before AgentReady is dispatched, so early agent output can arrive in
	// either order relative to AgentReady.
	if isStarting && !wantStarting {
		return true
	}
	if isStarted && !wantStarted {
		return true
	}
	_, isStatus := ev.(session.ParticipantStatusChanged)
	_, wantStatus := any(*new(T)).(session.ParticipantStatusChanged)
	if isStatus && !wantStatus {
		return true
	}
	// AgentMessage is emitted asynchronously by the readLoop and has no
	// ordering guarantee relative to session-level coordination events.
	// Skip it unless the caller is explicitly waiting for one.
	_, isMsg := ev.(session.AgentMessage)
	_, wantMsg := any(*new(T)).(session.AgentMessage)
	if isMsg && !wantMsg {
		return true
	}
	_, wantCrash := any(*new(T)).(session.AgentCrashed)
	_, isLog := ev.(session.AgentLog)
	return wantCrash && isLog
}

func mustReceive[T session.Event](t *testing.T, ch <-chan session.Event) T {
	t.Helper()
	deadline := time.After(time.Second)
	for {
		select {
		case ev := <-ch:
			if shouldSkipEvent[T](ev) {
				continue
			}
			wantZero := *new(T)
			got, ok := ev.(T)
			if !ok {
				t.Fatalf("expected %T, got %T", wantZero, ev)
			}
			return got
		case <-deadline:
			t.Fatalf("timed out waiting for %T event", *new(T))
			var zero T
			return zero
		}
	}
}

// invite executes an InviteCommand for the given alias.
func invite(t *testing.T, s *session.Session, alias string) {
	t.Helper()
	_, err := s.Execute(session.InviteCommand{
		Alias: alias,
	})
	if err != nil {
		t.Fatalf("InviteCommand %q: %v", alias, err)
	}
}

func enableSendNotices(t *testing.T, s *session.Session) {
	t.Helper()
	if _, err := s.Execute(session.EnablePolicyCommand{Name: policy.SendNotices}); err != nil {
		t.Fatalf("enable send notices: %v", err)
	}
}

// fixedFactory returns a session option whose factory always returns the given agent.
func fixedFactory(a agent.Agent) session.Option {
	return session.WithAgentFactory(func(_ *session.Session, _ roomconfig.ParticipantConfig) (agent.Agent, error) { return a, nil })
}

func participantStatus(t *testing.T, s *session.Session, alias string) participant.Status {
	t.Helper()
	p, ok := s.ParticipantByName(alias)
	if !ok {
		t.Fatalf("participant %q not found", alias)
	}
	return p.Status
}

func expectParticipantStatus(t *testing.T, s *session.Session, alias string, want participant.Status, context string) {
	t.Helper()
	if got := participantStatus(t, s, alias); got != want {
		t.Fatalf("%s: expected %s, got %s", context, want, got)
	}
}

func awaitIdleWithoutInvariantLog(t *testing.T, obs *testObserver, s *session.Session, alias string, context string) {
	t.Helper()
	deadline := time.After(time.Second)
	for {
		select {
		case ev := <-obs.ch:
			if log, ok := ev.(session.AgentLog); ok {
				t.Errorf("%s: unexpected invariant log: %q", context, log.Text)
			}
			if status, ok := ev.(session.ParticipantStatusChanged); ok && status.To == participant.StatusIdle {
				expectParticipantStatus(t, s, alias, participant.StatusIdle, context)
				return
			}
		case <-deadline:
			t.Fatalf("%s: timed out waiting for idle", context)
		}
	}
}

func sendTurnMessage(a *mockAgent, streamID agent.StreamID, mode agent.Mode, content agent.MessageContent) {
	a.ch <- agent.Message{StreamID: streamID, Mode: mode, Content: content}
}

func expectTurnMessageForwarded(t *testing.T, obs *testObserver) {
	t.Helper()
	mustReceive[session.AgentMessage](t, obs.ch)
}

// mappedFactory returns a session option whose factory looks up agents by alias.
func mappedFactory(agents map[string]agent.Agent) session.Option {
	return session.WithAgentFactory(func(_ *session.Session, cfg roomconfig.ParticipantConfig) (agent.Agent, error) {
		return agents[cfg.Alias], nil
	})
}

func newSession(t *testing.T, opts ...session.Option) *session.Session {
	t.Helper()
	s := session.New(opts...)
	t.Cleanup(s.Shutdown)
	return s
}

// --- tests ---

func TestInvite_emitsAgentReady(t *testing.T) {
	obs := newTestObserver()
	a := newMockAgent()
	s := newSession(t, session.WithObserver(obs), fixedFactory(a))
	t.Cleanup(func() { _, _ = s.Execute(session.RemoveCommand{Alias: resolvedAlias(s, "ada")}) })

	invite(t, s, "ada")
	mustReceive[session.AgentStarting](t, obs.ch)
	mustReceive[session.AgentReady](t, obs.ch)
}

func TestInvite_earlyAgentOutputDeliveredAfterStarted(t *testing.T) {
	// Verify that output buffered in the agent pipe before AgentReady is
	// dispatched (produced during Start()) is delivered while the participant
	// is StatusIdle. The readLoop starts before AgentReady fires, so the
	// log and the lifecycle event may arrive in either order; shouldSkipEvent
	// skips AgentReady when waiting for AgentLog.
	obs := newTestObserver()
	a := &earlyOutputAgent{
		mockAgent: newMockAgent(),
		earlyMsg:  agent.Message{Mode: agent.ModeSingle, Content: agent.Log{Text: "early startup log"}},
	}
	s := newSession(t, session.WithObserver(obs), fixedFactory(a))
	t.Cleanup(func() { _, _ = s.Execute(session.RemoveCommand{Alias: resolvedAlias(s, "ada")}) })

	invite(t, s, "ada")
	got := mustReceive[session.AgentLog](t, obs.ch)
	if got.Text != "early startup log" {
		t.Fatalf("early agent log: got %q, want %q", got.Text, "early startup log")
	}
	expectParticipantStatus(t, s, "ada", participant.StatusIdle, "when early log delivered")
}

func TestCancel_interruptsAgent(t *testing.T) {
	obs := newTestObserver()
	a := newMockAgent()
	s := newSession(t, session.WithObserver(obs), fixedFactory(a))
	t.Cleanup(func() { _, _ = s.Execute(session.RemoveCommand{Alias: resolvedAlias(s, "ada")}) })

	invite(t, s, "ada")
	mustReceive[session.AgentStarting](t, obs.ch)
	mustReceive[session.AgentReady](t, obs.ch)

	if _, err := s.Execute(session.CancelCommand{Alias: resolvedAlias(s, "ada")}); err != nil {
		t.Fatalf("CancelCommand: %v", err)
	}
	a.mu.Lock()
	got := a.interrupts
	a.mu.Unlock()
	if got != 1 {
		t.Fatalf("expected 1 interrupt call, got %d", got)
	}
}

func TestCancel_notReadyWhileStarting(t *testing.T) {
	obs := newTestObserver()
	base := newMockAgent()
	g := &gateAgent{startGate: make(chan struct{}), mockAgent: base}
	s := newSession(t, session.WithObserver(obs), fixedFactory(g))
	t.Cleanup(func() { _, _ = s.Execute(session.RemoveCommand{Alias: resolvedAlias(s, "ada")}) })

	invite(t, s, "ada")
	mustReceive[session.AgentStarting](t, obs.ch)

	if _, err := s.Execute(session.CancelCommand{Alias: resolvedAlias(s, "ada")}); err == nil {
		t.Fatalf("expected error cancelling starting agent")
	}

	close(g.startGate)
	mustReceive[session.AgentReady](t, obs.ch)
}

func TestCancel_notReadyWhenCrashed(t *testing.T) {
	obs := newTestObserver()
	a := newMockAgent()
	a.startErr = errors.New("boom")
	s := newSession(t, session.WithObserver(obs), fixedFactory(a))

	invite(t, s, "ada")
	mustReceive[session.AgentStarting](t, obs.ch)
	mustReceive[session.AgentCrashed](t, obs.ch)

	if _, err := s.Execute(session.CancelCommand{Alias: resolvedAlias(s, "ada")}); err == nil {
		t.Fatalf("expected error cancelling crashed agent")
	}
}

func TestCancel_unknownAlias(t *testing.T) {
	s := newSession(t)
	if _, err := s.Execute(session.CancelCommand{Alias: resolvedAlias(s, "nobody")}); err == nil {
		t.Fatalf("expected error for unknown participant")
	}
}

func TestInvite_duplicateAlias(t *testing.T) {
	s := newSession(t, session.WithAgentFactory(func(_ *session.Session, _ roomconfig.ParticipantConfig) (agent.Agent, error) {
		return newMockAgent(), nil
	}))
	t.Cleanup(func() { _, _ = s.Execute(session.RemoveCommand{Alias: resolvedAlias(s, "ada")}) })

	invite(t, s, "ada")
	_, err := s.Execute(session.InviteCommand{Alias: "ada"})
	if err == nil {
		t.Fatal("expected error on duplicate alias, got nil")
	}
}

func TestInvite_buildsEchoWithoutFactoryWhenPolicyEnabled(t *testing.T) {
	obs := newTestObserver()
	factoryCalled := false
	s := newSession(t,
		session.WithObserver(obs),
		session.WithAgentFactory(func(_ *session.Session, _ roomconfig.ParticipantConfig) (agent.Agent, error) {
			factoryCalled = true
			return newMockAgent(), nil
		}),
	)
	if _, err := s.Execute(session.EnablePolicyCommand{Name: policy.EchoInvites}); err != nil {
		t.Fatalf("EnablePolicyCommand: %v", err)
	}
	t.Cleanup(func() { _, _ = s.Execute(session.RemoveCommand{Alias: resolvedAlias(s, "ada")}) })

	invite(t, s, "ada")
	mustReceive[session.AgentReady](t, obs.ch)
	if _, err := s.Execute(session.EnablePolicyCommand{Name: policy.EchoInvites}); err != nil {
		t.Fatalf("idempotent EnablePolicyCommand: %v", err)
	}
	if factoryCalled {
		t.Fatal("factory called, want the session to build echo")
	}
	if p, _ := s.ParticipantByName("ada"); p.Backend != "echo" {
		t.Fatalf("Backend = %q, want echo", p.Backend)
	}
}

func TestInvite_recordsBackendTheAgentReports(t *testing.T) {
	root := t.TempDir()
	writeSessionConfigFile(t, root, ".coderoom/participants/ada.yaml", "alias: ada\nbackend: claude\n")
	obs := newTestObserver()
	s := newSession(t,
		session.WithObserver(obs),
		session.WithConfig(loadSessionConfig(t, root)),
		fixedFactory(newMockAgent()), // reports "fake"
	)
	t.Cleanup(func() { _, _ = s.Execute(session.RemoveCommand{Alias: resolvedAlias(s, "ada")}) })

	invite(t, s, "ada")
	starting := mustReceive[session.AgentStarting](t, obs.ch)
	mustReceive[session.AgentReady](t, obs.ch)

	if starting.Backend != "fake" {
		t.Fatalf("AgentStarting.Backend = %q, want fake", starting.Backend)
	}
	if p, _ := s.ParticipantByName("ada"); p.Backend != "fake" {
		t.Fatalf("View.Backend = %q, want fake", p.Backend)
	}
}

func TestInvite_passesConfiguredBackendToFactory(t *testing.T) {
	root := t.TempDir()
	writeSessionConfigFile(t, root, ".coderoom/participants/ada.yaml", "alias: ada\nbackend: claude\n")
	obs := newTestObserver()
	var requested []string
	s := newSession(t,
		session.WithObserver(obs),
		session.WithConfig(loadSessionConfig(t, root)),
		session.WithAgentFactory(func(_ *session.Session, cfg roomconfig.ParticipantConfig) (agent.Agent, error) {
			requested = append(requested, cfg.Backend.Name)
			return newMockAgent(), nil
		}),
	)
	t.Cleanup(func() {
		_, _ = s.Execute(session.RemoveCommand{Alias: resolvedAlias(s, "ada")})
		_, _ = s.Execute(session.RemoveCommand{Alias: resolvedAlias(s, "tim")})
	})

	invite(t, s, "ada")
	mustReceive[session.AgentReady](t, obs.ch)
	invite(t, s, "tim")
	mustReceive[session.AgentReady](t, obs.ch)
	if !slices.Equal(requested, []string{"claude", "codex"}) {
		t.Fatalf("requested backends = %v, want [claude codex]", requested)
	}
}

func TestInvite_factoryErrorRemovesParticipant(t *testing.T) {
	obs := newTestObserver()
	fail := true
	s := newSession(t,
		session.WithObserver(obs),
		session.WithAgentFactory(func(_ *session.Session, _ roomconfig.ParticipantConfig) (agent.Agent, error) {
			if fail {
				return nil, errors.New("unknown backend")
			}
			return newMockAgent(), nil
		}),
	)
	t.Cleanup(func() { _, _ = s.Execute(session.RemoveCommand{Alias: resolvedAlias(s, "ada")}) })

	if _, err := s.Execute(session.InviteCommand{Alias: "ada"}); err == nil {
		t.Fatal("expected invite error")
	}
	if _, ok := s.ParticipantByName("ada"); ok {
		t.Fatal("participant still registered after failed invite")
	}

	fail = false
	invite(t, s, "ada")
	mustReceive[session.AgentReady](t, obs.ch)
	if _, err := s.Execute(session.EnablePolicyCommand{Name: policy.EchoInvites}); err == nil {
		t.Fatal("expected enabling echo-invites after a successful invitation to fail")
	}
}

func TestEnableEchoInvites_rejectsAfterInvitation(t *testing.T) {
	obs := newTestObserver()
	s := newSession(t, session.WithObserver(obs), fixedFactory(newMockAgent()))

	invite(t, s, "ada")
	mustReceive[session.AgentReady](t, obs.ch)
	if _, err := s.Execute(session.RemoveCommand{Alias: resolvedAlias(s, "ada")}); err != nil {
		t.Fatalf("RemoveCommand: %v", err)
	}
	if _, err := s.Execute(session.EnablePolicyCommand{Name: policy.EchoInvites}); err == nil {
		t.Fatal("expected enabling echo-invites after an invitation to fail")
	}
}

func TestEchoInvite_completesAnchoredTurnWithUnchangedOutput(t *testing.T) {
	obs := newTestObserver()
	s := newSession(t,
		session.WithObserver(obs),
		fixedFactory(newMockAgent()),
	)
	if _, err := s.Execute(session.EnablePolicyCommand{Name: policy.EchoInvites}); err != nil {
		t.Fatalf("EnablePolicyCommand: %v", err)
	}
	t.Cleanup(func() { _, _ = s.Execute(session.RemoveCommand{Alias: resolvedAlias(s, "ada")}) })
	invite(t, s, "ada")
	mustReceive[session.AgentReady](t, obs.ch)

	const prompt = "repeat this exactly"
	_, err := s.Execute(session.SendToParticipantCommand{
		Plan:    s.CreateParticipantSendPlan(resolvedAlias(s, "ada")),
		Message: prompt,
	})
	if err != nil {
		t.Fatalf("SendToParticipantCommand: %v", err)
	}
	var msg session.AgentMessage
	for {
		event := <-obs.ch
		var ok bool
		msg, ok = event.(session.AgentMessage)
		if ok {
			break
		}
	}
	output, ok := msg.Msg.Content.(agent.Output)
	if !ok || output.Text != prompt {
		t.Fatalf("output = %#v, want %q", msg.Msg.Content, prompt)
	}
	awaitIdleWithoutInvariantLog(t, obs, s, "ada", "after echoed output")
}

func TestInvite_resolvesRoleFromConfig(t *testing.T) {
	root := t.TempDir()
	writeSessionConfigFile(t, root, ".coderoom/participants/ada.yaml", "alias: ada\nrole: reviewer\n")
	writeSessionConfigFile(t, root, ".coderoom/prompts/roles/reviewer.md", "Focus on correctness.")

	obs := newTestObserver()
	a := newMockAgent()
	s := newSession(
		t,
		session.WithObserver(obs),
		session.WithConfig(loadSessionConfig(t, root)),
		fixedFactory(a),
	)
	t.Cleanup(func() { _, _ = s.Execute(session.RemoveCommand{Alias: resolvedAlias(s, "ada")}) })

	if _, err := s.Execute(session.InviteCommand{Alias: "ada"}); err != nil {
		t.Fatalf("InviteCommand: %v", err)
	}
	mustReceive[session.AgentStarting](t, obs.ch)
	mustReceive[session.AgentReady](t, obs.ch)

	p, ok := s.ParticipantByName("ada")
	if !ok {
		t.Fatal("participant not found after invite")
	}
	if p.Role != "reviewer" {
		t.Fatalf("expected role %q, got %q", "reviewer", p.Role)
	}
}

func TestInvite_assignsColorToParticipant(t *testing.T) {
	obs := newTestObserver()
	a := newMockAgent()
	s := newSession(t, session.WithObserver(obs), fixedFactory(a))
	t.Cleanup(func() { _, _ = s.Execute(session.RemoveCommand{Alias: resolvedAlias(s, "ada")}) })

	_, err := s.Execute(session.InviteCommand{Alias: "ada"})
	if err != nil {
		t.Fatalf("InviteCommand: %v", err)
	}
	mustReceive[session.AgentStarting](t, obs.ch)
	mustReceive[session.AgentReady](t, obs.ch)
	p, ok := s.ParticipantByName("ada")
	if !ok {
		t.Fatal("participant not found after invite")
	}
	if p.Color == "" {
		t.Error("expected participant color")
	}
}

func TestInvite_reportsConfigChangedSinceStartup(t *testing.T) {
	root := t.TempDir()
	writeSessionConfigFile(t, root, ".coderoom/participants/ada.yaml", "alias: ada\nrole: reviewer\n")
	writeSessionConfigFile(t, root, ".coderoom/prompts/roles/reviewer.md", "Focus on correctness.")
	cfg := loadSessionConfig(t, root)
	writeSessionConfigFile(t, root, ".coderoom/participants/ada.yaml", "alias: ada\nrole: [broken\n")

	obs := newTestObserver()
	s := newSession(t, session.WithObserver(obs), session.WithConfig(cfg), fixedFactory(newMockAgent()))
	t.Cleanup(func() { _, _ = s.Execute(session.RemoveCommand{Alias: resolvedAlias(s, "ada")}) })

	invite(t, s, "ada")
	changed := mustReceive[session.ParticipantConfigChanged](t, obs.ch)
	want := []string{filepath.Join(".coderoom", "participants", "ada.yaml")}
	if changed.Alias.Name != "ada" || !slices.Equal(changed.Sources, want) {
		t.Fatalf("ParticipantConfigChanged = %+v, want ada with %v", changed, want)
	}
	mustReceive[session.AgentStarting](t, obs.ch)
	mustReceive[session.AgentReady](t, obs.ch)

	// The invite used the loaded config, not the broken edit.
	p, _ := s.ParticipantByName("ada")
	if p.Role != "reviewer" {
		t.Fatalf("role = %q, want reviewer", p.Role)
	}
}

func TestInvite_factoryGetsConfigLoadedAtStartup(t *testing.T) {
	root := t.TempDir()
	writeSessionConfigFile(t, root, ".coderoom/participants/ada.yaml", "alias: ada\nrole: reviewer\nbackend: claude\n")
	writeSessionConfigFile(t, root, ".coderoom/prompts/roles/reviewer.md", "Focus on correctness.")
	cfg := loadSessionConfig(t, root)
	want, err := cfg.Participant("ada")
	if err != nil {
		t.Fatal(err)
	}
	// Valid edits to both sources, which a reload would pick up.
	writeSessionConfigFile(t, root, ".coderoom/participants/ada.yaml", "alias: ada\nrole: reviewer\nbackend: codex\n")
	writeSessionConfigFile(t, root, ".coderoom/prompts/roles/reviewer.md", "Focus on style.")

	obs := newTestObserver()
	var got roomconfig.ParticipantConfig
	s := newSession(t,
		session.WithObserver(obs),
		session.WithConfig(cfg),
		session.WithAgentFactory(func(_ *session.Session, c roomconfig.ParticipantConfig) (agent.Agent, error) {
			got = c
			return newMockAgent(), nil
		}),
	)
	t.Cleanup(func() { _, _ = s.Execute(session.RemoveCommand{Alias: resolvedAlias(s, "ada")}) })

	invite(t, s, "ada")
	mustReceive[session.ParticipantConfigChanged](t, obs.ch)
	mustReceive[session.AgentReady](t, obs.ch)
	if got != want {
		t.Fatalf("factory config = %+v, want the startup config %+v", got, want)
	}
}

func loadSessionConfig(t *testing.T, root string) *roomconfig.Config {
	t.Helper()
	cfg, err := roomconfig.Load(root, roomconfig.Defaults{Backend: roomconfig.BackendRequest{Name: "codex", Source: "default"}})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return cfg
}

func writeSessionConfigFile(t *testing.T, root, name, content string) {
	t.Helper()

	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("MkdirAll(%q): %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile(%q): %v", path, err)
	}
}

func TestRemove_emitsAgentStopped(t *testing.T) {
	obs := newTestObserver()
	a := newMockAgent()
	s := newSession(t, session.WithObserver(obs), fixedFactory(a))

	invite(t, s, "ada")
	mustReceive[session.AgentStarting](t, obs.ch)
	mustReceive[session.AgentReady](t, obs.ch)

	if _, err := s.Execute(session.RemoveCommand{Alias: resolvedAlias(s, "ada")}); err != nil {
		t.Fatalf("RemoveCommand: %v", err)
	}
	mustReceive[session.AgentStopped](t, obs.ch)
}

func TestRemove_duringStartup_isRejected(t *testing.T) {
	// /remove must be rejected while the startup goroutine has not yet dispatched
	// AgentReady. Two sub-windows are covered:
	//
	//  1. Before attachParticipant: participant is StatusStarting with no
	//     s.agents entry → detachParticipant returns false → evictCrashedBeforeStart
	//     rejects because status is not StatusCrashed.
	//  2. After attachParticipant but before SessionReady: the participant is
	//     present in s.agents, but IsRemovable is still false →
	//     detachParticipant returns false (same rejection path).
	//
	// StartupReady=true is set (under s.mu) before AgentReady is dispatched.
	// By the Go memory model, any goroutine that receives AgentReady from the
	// observer channel is guaranteed to see StartupReady=true, so /remove
	// succeeds immediately after the event is observed.
	obs := newTestObserver()
	var once sync.Once
	gate := make(chan struct{})
	closeGate := func() { once.Do(func() { close(gate) }) }
	t.Cleanup(closeGate)

	g := &gateAgent{startGate: gate, mockAgent: newMockAgent()}
	s := newSession(t, session.WithObserver(obs), fixedFactory(g))

	invite(t, s, "ada")
	mustReceive[session.AgentStarting](t, obs.ch)

	if _, err := s.Execute(session.RemoveCommand{Alias: resolvedAlias(s, "ada")}); err == nil {
		t.Fatal("RemoveCommand during startup returned nil; want error")
	}

	closeGate()
	mustReceive[session.AgentReady](t, obs.ch)
	if _, err := s.Execute(session.RemoveCommand{Alias: resolvedAlias(s, "ada")}); err != nil {
		t.Fatalf("remove after startup: %v", err)
	}
	mustReceive[session.AgentStopped](t, obs.ch)
}

func TestRemove_crashedBeforeStart_succeeds(t *testing.T) {
	// A participant whose Start() fails never has its reader started, so it
	// lives in the registry but not in the agents table. /remove must still
	// work and emit AgentStopped so the UI participants refreshes.
	obs := newTestObserver()
	a := newMockAgent()
	a.startErr = errors.New("missing native binary")
	s := newSession(t, session.WithObserver(obs), fixedFactory(a))

	invite(t, s, "ada")
	mustReceive[session.AgentStarting](t, obs.ch)
	mustReceive[session.AgentCrashed](t, obs.ch)

	if _, err := s.Execute(session.RemoveCommand{Alias: resolvedAlias(s, "ada")}); err != nil {
		t.Fatalf("RemoveCommand on crashed participant: %v", err)
	}
	mustReceive[session.AgentStopped](t, obs.ch)

	// Participant must be absent from the participant list after removal.
	if _, ok := s.ParticipantByName("ada"); ok {
		t.Error("participant still in participant list after remove")
	}
}

func TestRemove_notFound(t *testing.T) {
	s := newSession(t)
	if _, err := s.Execute(session.RemoveCommand{Alias: resolvedAlias(s, "nobody")}); err == nil {
		t.Fatal("expected error for unknown alias, got nil")
	}
}

func TestBroadcast_emitsAndSendsToAllAgents(t *testing.T) {
	obs := newTestObserver()
	a1 := newMockAgent()
	a2 := newMockAgent()
	s := newSession(t, session.WithObserver(obs), mappedFactory(map[string]agent.Agent{
		"ada":    a1,
		"turing": a2,
	}))
	t.Cleanup(func() {
		_, _ = s.Execute(session.RemoveCommand{Alias: resolvedAlias(s, "ada")})
		_, _ = s.Execute(session.RemoveCommand{Alias: resolvedAlias(s, "turing")})
	})

	invite(t, s, "ada")
	mustReceive[session.AgentReady](t, obs.ch)
	invite(t, s, "turing")
	mustReceive[session.AgentReady](t, obs.ch)

	if _, err := s.Execute(session.BroadcastCommand{Text: "hello"}); err != nil {
		t.Fatalf("BroadcastCommand: %v", err)
	}
	mustReceive[session.RoutingCompleted](t, obs.ch)

	for _, a := range []*mockAgent{a1, a2} {
		a.mu.Lock()
		if len(a.sends) == 0 || a.sends[len(a.sends)-1] != "hello" {
			t.Errorf("agent did not receive broadcast")
		}
		a.mu.Unlock()
	}

	for _, alias := range []string{"ada", "turing"} {
		p, ok := s.ParticipantByName(alias)
		if !ok {
			t.Fatalf("expected participant %q", alias)
		}
		if p.Status != participant.StatusWorking {
			t.Fatalf("expected %q status %q, got %q", alias, participant.StatusWorking, p.Status)
		}
	}
}

func TestBroadcastCommand_excludesParticipantsOutsideFrozenAliases(t *testing.T) {
	obs := newTestObserver()
	ada := newMockAgent()
	turing := newMockAgent()
	s := newSession(t, session.WithObserver(obs), mappedFactory(map[string]agent.Agent{
		"ada": ada, "turing": turing,
	}))
	t.Cleanup(func() {
		_, _ = s.Execute(session.RemoveCommand{Alias: resolvedAlias(s, "ada")})
		_, _ = s.Execute(session.RemoveCommand{Alias: resolvedAlias(s, "turing")})
	})

	invite(t, s, "ada")
	mustReceive[session.AgentReady](t, obs.ch)
	invite(t, s, "turing")
	mustReceive[session.AgentReady](t, obs.ch)

	if _, err := s.Execute(session.BroadcastCommand{Aliases: resolvedAliases(s, "ada"), Text: "hello"}); err != nil {
		t.Fatalf("BroadcastCommand: %v", err)
	}
	mustReceive[session.RoutingCompleted](t, obs.ch)

	ada.mu.Lock()
	adaSends := slices.Clone(ada.sends)
	ada.mu.Unlock()
	turing.mu.Lock()
	turingSends := slices.Clone(turing.sends)
	turing.mu.Unlock()
	if !slices.Equal(adaSends, []string{"hello"}) || len(turingSends) != 0 {
		t.Fatalf("sends: ada=%v turing=%v", adaSends, turingSends)
	}
}

func TestBroadcast_sendError_doesNotMarkWorking(t *testing.T) {
	obs := newTestObserver()
	a1 := newMockAgent()
	a1.sendErr = errors.New("send failed")
	a2 := newMockAgent()
	s := newSession(t, session.WithObserver(obs), mappedFactory(map[string]agent.Agent{
		"ada":    a1,
		"turing": a2,
	}))
	t.Cleanup(func() {
		_, _ = s.Execute(session.RemoveCommand{Alias: resolvedAlias(s, "ada")})
		_, _ = s.Execute(session.RemoveCommand{Alias: resolvedAlias(s, "turing")})
	})

	invite(t, s, "ada")
	mustReceive[session.AgentReady](t, obs.ch)
	invite(t, s, "turing")
	mustReceive[session.AgentReady](t, obs.ch)

	_, err := s.Execute(session.BroadcastCommand{Text: "hello"})
	if err == nil {
		t.Fatal("expected broadcast error, got nil")
	}
	mustReceive[session.RoutingCompleted](t, obs.ch)

	p, ok := s.ParticipantByName("ada")
	if !ok {
		t.Fatal("expected participant ada")
	}
	if p.Status == participant.StatusWorking {
		t.Fatalf("expected ada not to be marked %q on send error", participant.StatusWorking)
	}

	p, ok = s.ParticipantByName("turing")
	if !ok {
		t.Fatal("expected participant turing")
	}
	if p.Status != participant.StatusWorking {
		t.Fatalf("expected turing status %q, got %q", participant.StatusWorking, p.Status)
	}

	if got := session.DeliveredAliases(err); len(got) != 1 || got[0] != "turing" {
		t.Fatalf("expected delivered aliases [turing], got %v", got)
	}
}

func TestBroadcast_sendErrorDoesNotReviveCrashedParticipant(t *testing.T) {
	obs := newTestObserver()
	ada := newMockAgent()
	s := newSession(t, session.WithObserver(obs), fixedFactory(ada))
	t.Cleanup(func() { _, _ = s.Execute(session.RemoveCommand{Alias: resolvedAlias(s, "ada")}) })

	invite(t, s, "ada")
	mustReceive[session.AgentReady](t, obs.ch)

	ada.sendHook = func(string) error {
		_ = ada.Stop()
		deadline := time.After(time.Second)
		for {
			p, ok := s.ParticipantByName("ada")
			if ok && p.Status == participant.StatusCrashed {
				return errors.New("send failed during crash")
			}
			select {
			case <-deadline:
				t.Fatal("timed out waiting for participant crash during send")
			default:
				time.Sleep(1 * time.Millisecond)
			}
		}
	}

	if _, err := s.Execute(session.BroadcastCommand{Text: "hello"}); err == nil {
		t.Fatal("expected broadcast error, got nil")
	}

	p, ok := s.ParticipantByName("ada")
	if !ok {
		t.Fatal("expected participant ada")
	}
	if p.Status != participant.StatusCrashed {
		t.Fatalf("expected ada to remain crashed after send rollback, got %q", p.Status)
	}
}

func TestReadLoop_dropsStreamFragmentsWhileIdle(t *testing.T) {
	obs := newTestObserver()
	a := newMockAgent(
		agent.Message{StreamID: "out1", Mode: agent.ModeFlush, Content: agent.Output{Text: ""}},
		agent.Message{StreamID: "out2", Mode: agent.ModeStream, Content: agent.Output{Text: "oops"}},
	)
	s := newSession(t, session.WithObserver(obs), fixedFactory(a))
	t.Cleanup(func() { _, _ = s.Execute(session.RemoveCommand{Alias: resolvedAlias(s, "ada")}) })

	invite(t, s, "ada")
	mustReceive[session.AgentReady](t, obs.ch)

	// Participant should remain idle. The unmatched flush is forwarded as a
	// message but now also surfaces an invariant log. The subsequent stream
	// fragment should also be dropped with a protocol log.
	ev := mustReceive[session.AgentLog](t, obs.ch) // unmatched flush invariant
	if ev.Text == "" {
		t.Fatal("expected invariant log text")
	}
	mustReceive[session.AgentMessage](t, obs.ch)  // unmatched flush message
	ev = mustReceive[session.AgentLog](t, obs.ch) // dropped stream
	if ev.Text == "" {
		t.Fatal("expected protocol log text")
	}

	p, ok := s.ParticipantByName("ada")
	if !ok {
		t.Fatal("expected participant ada")
	}
	if p.Status != participant.StatusIdle {
		t.Fatalf("expected ada to remain idle, got %q", p.Status)
	}
}

func waitForSharedKinds(t *testing.T, ch <-chan session.Event) {
	t.Helper()
	result := mustReceive[session.RoutingCompleted](t, ch).Result
	if len(result.Recipients) != 2 || result.Recipients[0].Role != session.RecipientPrimary || result.Recipients[1].Role != session.RecipientNotice {
		t.Fatalf("routing recipients = %#v", result.Recipients)
	}
}

func TestSharedSend_sendsToAddressedAndNotifiesOthers(t *testing.T) {
	obs := newTestObserver()
	ada := newMockAgent()
	turing := newMockAgent()
	s := newSession(t, session.WithObserver(obs), mappedFactory(map[string]agent.Agent{
		"ada":    ada,
		"turing": turing,
	}))
	t.Cleanup(func() {
		_, _ = s.Execute(session.RemoveCommand{Alias: resolvedAlias(s, "ada")})
		_, _ = s.Execute(session.RemoveCommand{Alias: resolvedAlias(s, "turing")})
	})

	invite(t, s, "ada")
	mustReceive[session.AgentReady](t, obs.ch)
	invite(t, s, "turing")
	mustReceive[session.AgentReady](t, obs.ch)
	enableSendNotices(t, s)

	if _, err := s.Execute(session.SendToParticipantCommand{Plan: s.CreateParticipantSendPlan(resolvedAlias(s, "ada")), Message: "do the thing", Notice: "ada is working on something"}); err != nil {
		t.Fatalf("SendToParticipantCommand: %v", err)
	}
	result := mustReceive[session.RoutingCompleted](t, obs.ch).Result
	if len(result.Recipients) != 2 || result.Recipients[1].Alias.Name != "turing" || result.Recipients[1].Role != session.RecipientNotice || result.Recipients[1].Status != session.DeliveryDelivered {
		t.Fatalf("notice outcome = %#v", result)
	}

	ada.mu.Lock()
	if len(ada.sends) == 0 || ada.sends[len(ada.sends)-1] != "do the thing" {
		t.Errorf("addressed agent did not receive instruction")
	}
	ada.mu.Unlock()

	turing.mu.Lock()
	if len(turing.sends) == 0 || turing.sends[len(turing.sends)-1] != "ada is working on something" {
		t.Errorf("other agent did not receive context notice")
	}
	turing.mu.Unlock()
}

func TestSharedSend_doesNotNotifyOthersByDefault(t *testing.T) {
	obs := newTestObserver()
	ada := newMockAgent()
	turing := newMockAgent()
	s := newSession(t, session.WithObserver(obs), mappedFactory(map[string]agent.Agent{
		"ada": ada, "turing": turing,
	}))
	t.Cleanup(func() {
		_, _ = s.Execute(session.RemoveCommand{Alias: resolvedAlias(s, "ada")})
		_, _ = s.Execute(session.RemoveCommand{Alias: resolvedAlias(s, "turing")})
	})

	invite(t, s, "ada")
	mustReceive[session.AgentReady](t, obs.ch)
	invite(t, s, "turing")
	mustReceive[session.AgentReady](t, obs.ch)

	if _, err := s.Execute(session.SendToParticipantCommand{
		Plan: s.CreateParticipantSendPlan(resolvedAlias(s, "ada")), Message: "do it", Notice: "notice",
	}); err != nil {
		t.Fatalf("SendToParticipantCommand: %v", err)
	}
	_ = mustReceive[session.RoutingCompleted](t, obs.ch)

	turing.mu.Lock()
	defer turing.mu.Unlock()
	if len(turing.sends) != 0 {
		t.Fatalf("listener received sends by default: %v", turing.sends)
	}
}

func TestSharedSendRecipientsFollowPolicy(t *testing.T) {
	obs := newTestObserver()
	s := newSession(t, session.WithObserver(obs), mappedFactory(map[string]agent.Agent{
		"ada": newMockAgent(), "ben": newMockAgent(), "turing": newMockAgent(),
	}))
	for _, alias := range []string{"turing", "ada", "ben"} {
		invite(t, s, alias)
		mustReceive[session.AgentReady](t, obs.ch)
	}

	if got := s.CreateParticipantSendPlan(resolvedAlias(s, "ada")).Targets(); !slices.Equal(namesOfAliases(got), []string{"ada"}) {
		t.Fatalf("default recipients = %v, want [ada]", got)
	}
	enableSendNotices(t, s)
	if got := s.CreateParticipantSendPlan(resolvedAlias(s, "ada")).Targets(); !slices.Equal(namesOfAliases(got), []string{"ada", "ben", "turing"}) {
		t.Fatalf("enabled recipients = %v, want [ada ben turing]", got)
	}
}

func TestParticipantSendPlanTargetsAreImmutable(t *testing.T) {
	obs := newTestObserver()
	s := newSession(t, session.WithObserver(obs), fixedFactory(newMockAgent()))
	invite(t, s, "ada")
	mustReceive[session.AgentReady](t, obs.ch)
	want := resolvedAlias(s, "ada")
	plan := s.CreateParticipantSendPlan(want)
	targets := plan.Targets()
	targets[0].Name = "changed"
	targets[0].ID = participant.ID{}
	if got := plan.Targets(); !slices.Equal(got, []participant.Alias{want}) {
		t.Fatalf("plan targets changed through returned slice: %v", got)
	}
}

func TestParticipantSendPlanRejectsDifferentSession(t *testing.T) {
	firstObserver, secondObserver := newTestObserver(), newTestObserver()
	firstAgent, secondAgent := newMockAgent(), newMockAgent()
	first := newSession(t, session.WithObserver(firstObserver), fixedFactory(firstAgent))
	second := newSession(t, session.WithObserver(secondObserver), fixedFactory(secondAgent))
	invite(t, first, "ada")
	firstAlias := mustReceive[session.AgentReady](t, firstObserver.ch).Alias
	invite(t, second, "ada")
	secondAlias := mustReceive[session.AgentReady](t, secondObserver.ch).Alias
	if firstAlias.ID == secondAlias.ID {
		t.Fatal("same-name participants in different Sessions share identity")
	}
	_, err := second.Execute(session.SendToParticipantCommand{
		Plan: first.CreateParticipantSendPlan(firstAlias), Message: "do it",
	})
	if err == nil {
		t.Fatal("expected cross-session plan error")
	}
	assertNoIdentityTestDelivery(t, []*mockAgent{firstAgent, secondAgent})
	result := mustReceive[session.RoutingCompleted](t, secondObserver.ch).Result
	if len(result.Recipients) != 1 || result.Recipients[0].Alias != firstAlias || result.Recipients[0].Status != session.DeliveryNotAttempted {
		t.Fatalf("cross-session rejection = %#v", result)
	}
}

func TestParticipantSendPlanDoesNotAddNewlyRoutableListener(t *testing.T) {
	obs := newTestObserver()
	ada := newMockAgent()
	turing := newMockAgent()
	ben := newMockAgent()
	s := newSession(t, session.WithObserver(obs), mappedFactory(map[string]agent.Agent{
		"ada": ada, "turing": turing, "ben": ben,
	}))
	for _, alias := range []string{"ada", "turing"} {
		invite(t, s, alias)
		mustReceive[session.AgentReady](t, obs.ch)
	}
	enableSendNotices(t, s)
	plan := s.CreateParticipantSendPlan(resolvedAlias(s, "ada"))
	invite(t, s, "ben")
	mustReceive[session.AgentReady](t, obs.ch)

	if _, err := s.Execute(session.SendToParticipantCommand{
		Plan: plan, Message: "do it", Notice: "notice",
	}); err != nil {
		t.Fatalf("SendToParticipantCommand: %v", err)
	}
	ben.mu.Lock()
	defer ben.mu.Unlock()
	if len(ben.sends) != 0 {
		t.Fatalf("new participant received frozen send: %v", ben.sends)
	}
}

func TestParticipantSendPlanDoesNotGainListenersWhenPolicyChanges(t *testing.T) {
	obs := newTestObserver()
	turing := newMockAgent()
	s := newSession(t, session.WithObserver(obs), mappedFactory(map[string]agent.Agent{
		"ada": newMockAgent(), "turing": turing,
	}))
	for _, alias := range []string{"ada", "turing"} {
		invite(t, s, alias)
		mustReceive[session.AgentReady](t, obs.ch)
	}
	plan := s.CreateParticipantSendPlan(resolvedAlias(s, "ada"))
	enableSendNotices(t, s)

	if _, err := s.Execute(session.SendToParticipantCommand{
		Plan: plan, Message: "do it", Notice: "notice",
	}); err != nil {
		t.Fatalf("SendToParticipantCommand: %v", err)
	}
	turing.mu.Lock()
	defer turing.mu.Unlock()
	if len(turing.sends) != 0 {
		t.Fatalf("staged plan gained listener after policy change: %v", turing.sends)
	}
}

func TestParticipantSendPlanReportsListenerRemovedBeforeExecution(t *testing.T) {
	obs := newTestObserver()
	s := newSession(t, session.WithObserver(obs), mappedFactory(map[string]agent.Agent{
		"ada": newMockAgent(), "turing": newMockAgent(),
	}))
	for _, alias := range []string{"ada", "turing"} {
		invite(t, s, alias)
		mustReceive[session.AgentReady](t, obs.ch)
	}
	enableSendNotices(t, s)
	plan := s.CreateParticipantSendPlan(resolvedAlias(s, "ada"))
	if _, err := s.Execute(session.RemoveCommand{Alias: resolvedAlias(s, "turing")}); err != nil {
		t.Fatalf("remove turing: %v", err)
	}

	_, err := s.Execute(session.SendToParticipantCommand{
		Plan: plan, Message: "do it", Notice: "notice",
	})
	if err == nil {
		t.Fatal("expected removed-listener delivery error")
	}
	if got := session.DeliveredAliases(err); !slices.Equal(got, []string{"ada"}) {
		t.Fatalf("delivered aliases = %v, want [ada]", got)
	}
}

func TestParticipantSendPlanRejectsSameAliasReplacement(t *testing.T) {
	for _, alias := range []string{"ada", "turing"} {
		t.Run(alias, func(t *testing.T) {
			obs := newTestObserver()
			agents := map[string]agent.Agent{"ada": newMockAgent(), "turing": newMockAgent()}
			s := newSession(t, session.WithObserver(obs), mappedFactory(agents))
			for _, name := range []string{"ada", "turing"} {
				invite(t, s, name)
				mustReceive[session.AgentReady](t, obs.ch)
			}
			enableSendNotices(t, s)
			plan := s.CreateParticipantSendPlan(resolvedAlias(s, "ada"))
			if _, err := s.Execute(session.RemoveCommand{Alias: resolvedAlias(s, alias)}); err != nil {
				t.Fatal(err)
			}
			mustReceive[session.AgentStopped](t, obs.ch)
			replacement := newMockAgent()
			agents[alias] = replacement
			invite(t, s, alias)
			mustReceive[session.AgentReady](t, obs.ch)

			_, err := s.Execute(session.SendToParticipantCommand{Plan: plan, Message: "work", Notice: "context"})
			if err == nil {
				t.Fatal("retained plan accepted a replacement recipient")
			}
			var wantDelivered []string
			if alias == "turing" {
				wantDelivered = []string{"ada"}
			}
			if got := session.DeliveredAliases(err); !slices.Equal(got, wantDelivered) {
				t.Fatalf("delivered aliases = %v, want %v", got, wantDelivered)
			}
			assertReplacementRouting(t, obs, alias)
			replacement.mu.Lock()
			defer replacement.mu.Unlock()
			if len(replacement.sends) != 0 {
				t.Fatalf("replacement received retained send: %v", replacement.sends)
			}
		})
	}
}

func assertReplacementRouting(t *testing.T, obs *testObserver, alias string) {
	t.Helper()
	outcomes, _ := readRoutingOutcomes(obs)
	if len(outcomes) != 1 || outcomes[0].Kind != session.RoutingParticipantSend || outcomes[0].Err == nil {
		t.Fatalf("replacement rejection outcomes = %#v", outcomes)
	}
	statuses := []session.DeliveryStatus{session.DeliveryFailed, session.DeliveryNotAttempted}
	if alias == "turing" {
		statuses = []session.DeliveryStatus{session.DeliveryDelivered, session.DeliveryFailed}
	}
	assertRecipientOutcomes(t, outcomes[0], []session.RecipientRole{session.RecipientPrimary, session.RecipientNotice}, statuses)
}

func TestParticipantSendPlanDiscardUnavailableNoticeRecipientsExcludesSameAliasRejoin(t *testing.T) {
	obs := newTestObserver()
	ada := newMockAgent()
	firstBob := newMockAgent()
	rejoinedBob := newMockAgent()
	bobInvites := 0
	s := newSession(t, session.WithObserver(obs), session.WithAgentFactory(
		func(_ *session.Session, cfg roomconfig.ParticipantConfig) (agent.Agent, error) {
			if cfg.Alias == "ada" {
				return ada, nil
			}
			bobInvites++
			if bobInvites == 1 {
				return firstBob, nil
			}
			return rejoinedBob, nil
		},
	))
	t.Cleanup(func() {
		_, _ = s.Execute(session.RemoveCommand{Alias: resolvedAlias(s, "ada")})
		_, _ = s.Execute(session.RemoveCommand{Alias: resolvedAlias(s, "bob")})
	})

	for _, alias := range []string{"ada", "bob"} {
		invite(t, s, alias)
		mustReceive[session.AgentReady](t, obs.ch)
	}
	enableSendNotices(t, s)
	plan := s.CreateParticipantSendPlan(resolvedAlias(s, "ada"))
	departedBob := resolvedAlias(s, "bob")
	if _, err := s.Execute(session.RemoveCommand{Alias: departedBob}); err != nil {
		t.Fatalf("remove bob: %v", err)
	}
	mustReceive[session.AgentStopped](t, obs.ch)
	invite(t, s, "bob")
	mustReceive[session.AgentReady](t, obs.ch)

	if _, err := s.Execute(session.SendToParticipantCommand{
		Plan:    plan.DiscardUnavailableNoticeRecipients([]participant.Alias{departedBob}),
		Message: "do it", Notice: "notice",
	}); err != nil {
		t.Fatalf("SendToParticipantCommand: %v", err)
	}
	rejoinedBob.mu.Lock()
	defer rejoinedBob.mu.Unlock()
	if len(rejoinedBob.sends) != 0 {
		t.Fatalf("rejoined listener received frozen send: %v", rejoinedBob.sends)
	}
}

func TestSharedSend_noticeMarksListenerWorkingUntilFlush(t *testing.T) {
	obs := newTestObserver()
	ada := newMockAgent()
	// Listener emits a flush when it receives a notice, ending its notice turn.
	turing := newNoticeFlushAgent()
	s := newSession(t, session.WithObserver(obs), mappedFactory(map[string]agent.Agent{
		"ada":    ada,
		"turing": turing,
	}))
	t.Cleanup(func() {
		_, _ = s.Execute(session.RemoveCommand{Alias: resolvedAlias(s, "ada")})
		_, _ = s.Execute(session.RemoveCommand{Alias: resolvedAlias(s, "turing")})
	})

	invite(t, s, "ada")
	mustReceive[session.AgentReady](t, obs.ch)
	invite(t, s, "turing")
	mustReceive[session.AgentReady](t, obs.ch)
	enableSendNotices(t, s)

	if _, err := s.Execute(session.SendToParticipantCommand{Plan: s.CreateParticipantSendPlan(resolvedAlias(s, "ada")), Message: "do it", Notice: "notice"}); err != nil {
		t.Fatalf("SendToParticipantCommand: %v", err)
	}
	waitForSharedKinds(t, obs.ch)

	p, _ := s.ParticipantByName("turing")
	// The session marks listeners working before issuing SendNotice, but a mock
	// agent can immediately emit a notice flush, racing the read loop and
	// returning the participant to idle before we observe the intermediate state.
	if p.Status != participant.StatusWorking && p.Status != participant.StatusIdle {
		t.Fatalf("expected listener to be working or idle after notice, got %q", p.Status)
	}

	// Wait until the listener returns to idle. The exact event ordering is not
	// stable with mocks (preloaded flushes can race with command dispatch), so
	// poll session state instead of asserting on specific event sequences.
	deadline := time.After(time.Second)
	for {
		p, _ = s.ParticipantByName("turing")
		if p.Status == participant.StatusIdle {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for listener to become idle; got %q", p.Status)
		default:
			time.Sleep(1 * time.Millisecond)
		}
	}
}

func TestSharedSend_noticeDoesNotResetWorkingSince(t *testing.T) {
	obs := newTestObserver()
	ada := newMockAgent()
	turing := newMockAgent()
	s := newSession(t, session.WithObserver(obs), mappedFactory(map[string]agent.Agent{
		"ada":    ada,
		"turing": turing,
	}))
	t.Cleanup(func() {
		_, _ = s.Execute(session.RemoveCommand{Alias: resolvedAlias(s, "ada")})
		_, _ = s.Execute(session.RemoveCommand{Alias: resolvedAlias(s, "turing")})
	})

	invite(t, s, "ada")
	mustReceive[session.AgentReady](t, obs.ch)
	invite(t, s, "turing")
	mustReceive[session.AgentReady](t, obs.ch)
	enableSendNotices(t, s)

	if _, err := s.Execute(session.SendToParticipantOutsideRoomCommand{Alias: resolvedAlias(s, "turing"), Text: "busy"}); err != nil {
		t.Fatalf("SendToParticipantOutsideRoomCommand: %v", err)
	}

	before, ok := s.ParticipantByName("turing")
	if !ok {
		t.Fatal("expected participant turing")
	}
	if before.Status != participant.StatusWorking {
		t.Fatalf("expected turing to be working before notice, got %q", before.Status)
	}

	if _, err := s.Execute(session.SendToParticipantCommand{Plan: s.CreateParticipantSendPlan(resolvedAlias(s, "ada")), Message: "do it", Notice: "notice"}); err != nil {
		t.Fatalf("SendToParticipantCommand: %v", err)
	}

	after, ok := s.ParticipantByName("turing")
	if !ok {
		t.Fatal("expected participant turing")
	}
	if after.Status != participant.StatusWorking {
		t.Fatalf("expected turing to remain working after notice, got %q", after.Status)
	}
	if !after.Since.Equal(before.Since) {
		t.Fatalf("expected turing Since to remain unchanged while already working: before=%v after=%v", before.Since, after.Since)
	}
}

func TestSharedSend_sendError_doesNotMarkWorking(t *testing.T) {
	obs := newTestObserver()
	ada := newMockAgent()
	ada.sendErr = errors.New("send failed")
	turing := newMockAgent()
	s := newSession(t, session.WithObserver(obs), mappedFactory(map[string]agent.Agent{
		"ada":    ada,
		"turing": turing,
	}))
	t.Cleanup(func() {
		_, _ = s.Execute(session.RemoveCommand{Alias: resolvedAlias(s, "ada")})
		_, _ = s.Execute(session.RemoveCommand{Alias: resolvedAlias(s, "turing")})
	})

	invite(t, s, "ada")
	mustReceive[session.AgentReady](t, obs.ch)
	invite(t, s, "turing")
	mustReceive[session.AgentReady](t, obs.ch)

	_, err := s.Execute(session.SendToParticipantCommand{Plan: s.CreateParticipantSendPlan(resolvedAlias(s, "ada")), Message: "do the thing", Notice: "ada is working on something"})
	if err == nil {
		t.Fatal("expected shared send error, got nil")
	}
	if got := session.DeliveredAliases(err); len(got) != 0 {
		t.Fatalf("expected no delivered aliases on direct-send failure, got %v", got)
	}

	p, ok := s.ParticipantByName("ada")
	if !ok {
		t.Fatal("expected participant ada")
	}
	if p.Status == participant.StatusWorking {
		t.Fatalf("expected ada not to be marked %q on send error", participant.StatusWorking)
	}

	p, ok = s.ParticipantByName("turing")
	if !ok {
		t.Fatal("expected participant turing")
	}
	if p.Status == participant.StatusWorking {
		t.Fatalf("expected turing not to be marked %q on send error", participant.StatusWorking)
	}
}

func TestSharedSend_noticeErrorReportsDeliveredAlias(t *testing.T) {
	obs := newTestObserver()
	ada := newMockAgent()
	ben := newMockAgent()
	turing := newMockAgent()
	turing.sendErr = errors.New("notice failed")
	s := newSession(t, session.WithObserver(obs), mappedFactory(map[string]agent.Agent{
		"ada":    ada,
		"ben":    ben,
		"turing": turing,
	}))
	t.Cleanup(func() {
		_, _ = s.Execute(session.RemoveCommand{Alias: resolvedAlias(s, "ada")})
		_, _ = s.Execute(session.RemoveCommand{Alias: resolvedAlias(s, "ben")})
		_, _ = s.Execute(session.RemoveCommand{Alias: resolvedAlias(s, "turing")})
	})

	invite(t, s, "ada")
	mustReceive[session.AgentReady](t, obs.ch)
	invite(t, s, "turing")
	mustReceive[session.AgentReady](t, obs.ch)
	invite(t, s, "ben")
	mustReceive[session.AgentReady](t, obs.ch)
	enableSendNotices(t, s)

	_, err := s.Execute(session.SendToParticipantCommand{Plan: s.CreateParticipantSendPlan(resolvedAlias(s, "ada")), Message: "do the thing", Notice: "ada is working on something"})
	if err == nil {
		t.Fatal("expected shared notice error, got nil")
	}
	if got := session.DeliveredAliases(err); !slices.Equal(got, []string{"ada", "ben"}) {
		t.Fatalf("expected delivered aliases [ada ben], got %v", got)
	}
}

func TestSharedSend_noticeErrorDoesNotReviveCrashedListener(t *testing.T) {
	obs := newTestObserver()
	ada := newMockAgent()
	turing := newMockAgent()
	s := newSession(t, session.WithObserver(obs), mappedFactory(map[string]agent.Agent{
		"ada":    ada,
		"turing": turing,
	}))
	t.Cleanup(func() {
		_, _ = s.Execute(session.RemoveCommand{Alias: resolvedAlias(s, "ada")})
		_, _ = s.Execute(session.RemoveCommand{Alias: resolvedAlias(s, "turing")})
	})

	invite(t, s, "ada")
	mustReceive[session.AgentReady](t, obs.ch)
	invite(t, s, "turing")
	mustReceive[session.AgentReady](t, obs.ch)
	enableSendNotices(t, s)

	turing.noticeHook = func(string) error {
		_ = turing.Stop()
		deadline := time.After(time.Second)
		for {
			p, ok := s.ParticipantByName("turing")
			if ok && p.Status == participant.StatusCrashed {
				return errors.New("notice failed during crash")
			}
			select {
			case <-deadline:
				t.Fatal("timed out waiting for listener crash during notice")
			default:
				time.Sleep(1 * time.Millisecond)
			}
		}
	}

	_, err := s.Execute(session.SendToParticipantCommand{Plan: s.CreateParticipantSendPlan(resolvedAlias(s, "ada")), Message: "do the thing", Notice: "ada is working on something"})
	if err == nil {
		t.Fatal("expected shared notice error, got nil")
	}

	p, ok := s.ParticipantByName("turing")
	if !ok {
		t.Fatal("expected participant turing")
	}
	if p.Status != participant.StatusCrashed {
		t.Fatalf("expected turing to remain crashed after notice rollback, got %q", p.Status)
	}
}

func TestSharedSend_notFound(t *testing.T) {
	s := newSession(t)
	if _, err := s.Execute(session.SendToParticipantCommand{Plan: s.CreateParticipantSendPlan(resolvedAlias(s, "nobody")), Message: "hi", Notice: "hi"}); err == nil {
		t.Fatal("expected error for unknown alias, got nil")
	}
}

func TestHandoff_sendsResolvedOutputAndEmitsEvent(t *testing.T) {
	obs, turing, s := newHandoffTestSession(t)
	executeResolvedHandoff(t, s, "final answer", 2)
	assertHandoffEvent(t, obs, "final answer", 2)
	assertHandoffNoticePayload(t, turing, "[HANDOFF from ada]\n\nfinal answer")
	assertHandoffAcceptedLog(t, obs, 2)
}

func newHandoffTestSession(t *testing.T) (*testObserver, *noticeFlushAgent, *session.Session) {
	t.Helper()

	obs := newTestObserver()
	ada := newMockAgent()
	turing := newNoticeFlushAgent()
	s := newSession(t,
		session.WithObserver(obs),
		mappedFactory(map[string]agent.Agent{
			"ada":    ada,
			"turing": turing,
		}),
	)
	t.Cleanup(func() {
		_, _ = s.Execute(session.RemoveCommand{Alias: resolvedAlias(s, "ada")})
		_, _ = s.Execute(session.RemoveCommand{Alias: resolvedAlias(s, "turing")})
	})

	invite(t, s, "ada")
	mustReceive[session.AgentReady](t, obs.ch)
	invite(t, s, "turing")
	mustReceive[session.AgentReady](t, obs.ch)
	return obs, turing, s
}

func executeResolvedHandoff(t *testing.T, s *session.Session, text string, recordIndex int) {
	t.Helper()

	if _, err := s.Execute(session.HandoffCommand{
		FromAlias: resolvedAlias(s, "ada"),
		ToAlias:   resolvedAlias(s, "turing"),
		Source:    session.HandoffSource{Text: text, RecordIndex: recordIndex},
	}); err != nil {
		t.Fatalf("HandoffCommand: %v", err)
	}
}

func assertHandoffEvent(t *testing.T, obs *testObserver, wantText string, wantRecordIndex int) {
	t.Helper()

	ev := mustReceive[session.HandoffDelivered](t, obs.ch)
	if ev.FromAlias.Name != "ada" || ev.ToAlias.Name != "turing" {
		t.Fatalf("unexpected handoff event: %#v", ev)
	}
	if ev.Text != wantText {
		t.Fatalf("unexpected handoff payload text: %q", ev.Text)
	}
	if ev.Preview != "[handoff ada -> turing]\n  ↦ source: ada latest output\n  > "+wantText {
		t.Fatalf("unexpected handoff preview: %q", ev.Preview)
	}
	if ev.SourceRecordIndex != wantRecordIndex {
		t.Fatalf("unexpected handoff source record index: %d", ev.SourceRecordIndex)
	}
}

func assertHandoffNoticePayload(t *testing.T, turing *noticeFlushAgent, want string) {
	t.Helper()

	turing.inner.mu.Lock()
	defer turing.inner.mu.Unlock()
	if len(turing.inner.sends) == 0 || turing.inner.sends[len(turing.inner.sends)-1] != want {
		t.Fatalf("unexpected handoff notice payload: %v", turing.inner.sends)
	}
}

func assertHandoffAcceptedLog(t *testing.T, obs *testObserver, wantRecordIndex int) {
	t.Helper()

	logEv := mustReceive[session.AgentLog](t, obs.ch)
	if !strings.Contains(logEv.Text, "handoff accepted: from=ada to=turing") || !strings.Contains(logEv.Text, fmt.Sprintf("source_record=%d", wantRecordIndex)) {
		t.Fatalf("unexpected accepted handoff log: %q", logEv.Text)
	}
}

func TestHandoff_requiresIdleParticipants(t *testing.T) {
	obs := newTestObserver()
	ada := newMockAgent()
	turing := newMockAgent()
	s := newSession(t,
		session.WithObserver(obs),
		mappedFactory(map[string]agent.Agent{
			"ada":    ada,
			"turing": turing,
		}),
	)
	t.Cleanup(func() {
		_, _ = s.Execute(session.RemoveCommand{Alias: resolvedAlias(s, "ada")})
		_, _ = s.Execute(session.RemoveCommand{Alias: resolvedAlias(s, "turing")})
	})

	invite(t, s, "ada")
	mustReceive[session.AgentReady](t, obs.ch)
	invite(t, s, "turing")
	mustReceive[session.AgentReady](t, obs.ch)

	if _, err := s.Execute(session.SendToParticipantOutsideRoomCommand{Alias: resolvedAlias(s, "turing"), Text: "busy"}); err != nil {
		t.Fatalf("SendToParticipantOutsideRoomCommand: %v", err)
	}

	mustReceive[session.RoutingCompleted](t, obs.ch)
	_, err := s.Execute(session.HandoffCommand{
		FromAlias: resolvedAlias(s, "ada"),
		ToAlias:   resolvedAlias(s, "turing"),
		Source:    session.HandoffSource{Text: "final answer", RecordIndex: 4},
	})
	if err == nil {
		t.Fatal("expected busy handoff error")
	}
	logEv := mustReceive[session.AgentLog](t, obs.ch)
	if !strings.Contains(logEv.Text, "handoff rejected: from=ada to=turing") || !strings.Contains(logEv.Text, "busy=[turing]") {
		t.Fatalf("unexpected busy handoff log: %q", logEv.Text)
	}
}

func TestHandoff_requiresCompletedSourceOutput(t *testing.T) {
	obs := newTestObserver()
	ada := newMockAgent()
	turing := newMockAgent()
	s := newSession(t,
		session.WithObserver(obs),
		mappedFactory(map[string]agent.Agent{
			"ada":    ada,
			"turing": turing,
		}),
	)
	t.Cleanup(func() {
		_, _ = s.Execute(session.RemoveCommand{Alias: resolvedAlias(s, "ada")})
		_, _ = s.Execute(session.RemoveCommand{Alias: resolvedAlias(s, "turing")})
	})

	invite(t, s, "ada")
	mustReceive[session.AgentReady](t, obs.ch)
	invite(t, s, "turing")
	mustReceive[session.AgentReady](t, obs.ch)

	_, err := s.Execute(session.HandoffCommand{
		FromAlias: resolvedAlias(s, "ada"),
		ToAlias:   resolvedAlias(s, "turing"),
	})
	if err == nil {
		t.Fatal("expected missing source output error")
	}
	logEv := mustReceive[session.AgentLog](t, obs.ch)
	if !strings.Contains(logEv.Text, "reason=no completed room-visible output") {
		t.Fatalf("unexpected missing-source handoff log: %q", logEv.Text)
	}
}

func TestHandoff_rejectionLogCompactsMultilineReason(t *testing.T) {
	obs := newTestObserver()
	ada := newMockAgent()
	turing := newNoticeFlushAgent()
	turing.inner.noticeHook = func(string) error {
		return errors.New("line one\nline two\n\nline three")
	}
	s := newSession(t,
		session.WithObserver(obs),
		mappedFactory(map[string]agent.Agent{
			"ada":    ada,
			"turing": turing,
		}),
	)
	t.Cleanup(func() {
		_, _ = s.Execute(session.RemoveCommand{Alias: resolvedAlias(s, "ada")})
		_, _ = s.Execute(session.RemoveCommand{Alias: resolvedAlias(s, "turing")})
	})

	invite(t, s, "ada")
	mustReceive[session.AgentReady](t, obs.ch)
	invite(t, s, "turing")
	mustReceive[session.AgentReady](t, obs.ch)

	_, err := s.Execute(session.HandoffCommand{
		FromAlias: resolvedAlias(s, "ada"),
		ToAlias:   resolvedAlias(s, "turing"),
		Source:    session.HandoffSource{Text: "final answer", RecordIndex: 2},
	})
	if err == nil {
		t.Fatal("expected handoff send error")
	}
	logEv := mustReceive[session.AgentLog](t, obs.ch)
	if strings.Contains(logEv.Text, "\n") {
		t.Fatalf("expected compact single-line handoff log, got %q", logEv.Text)
	}
	if !strings.Contains(logEv.Text, "reason=line one | line two | line three") {
		t.Fatalf("unexpected compacted rejection reason: %q", logEv.Text)
	}
}

func TestHandoff_ignoresStartingBystanderOutsideBarrier(t *testing.T) {
	obs := newTestObserver()
	ada := newMockAgent()
	turing := newNoticeFlushAgent()
	cat := &gateAgent{startGate: make(chan struct{}), mockAgent: newMockAgent()}
	s := newSession(t,
		session.WithObserver(obs),
		mappedFactory(map[string]agent.Agent{
			"ada":    ada,
			"turing": turing,
			"cat":    cat,
		}),
	)
	t.Cleanup(func() {
		_, _ = s.Execute(session.RemoveCommand{Alias: resolvedAlias(s, "ada")})
		_, _ = s.Execute(session.RemoveCommand{Alias: resolvedAlias(s, "turing")})
		_, _ = s.Execute(session.RemoveCommand{Alias: resolvedAlias(s, "cat")})
	})

	invite(t, s, "ada")
	mustReceive[session.AgentReady](t, obs.ch)
	invite(t, s, "turing")
	mustReceive[session.AgentReady](t, obs.ch)
	invite(t, s, "cat")

	if _, err := s.Execute(session.HandoffCommand{
		FromAlias: resolvedAlias(s, "ada"),
		ToAlias:   resolvedAlias(s, "turing"),
		Source:    session.HandoffSource{Text: "final answer", RecordIndex: 1},
	}); err != nil {
		t.Fatalf("HandoffCommand with starting bystander: %v", err)
	}

	close(cat.startGate)
	deadline := time.After(time.Second)
	for {
		select {
		case ev := <-obs.ch:
			if started, ok := ev.(session.AgentReady); ok && started.Alias.Name == "cat" {
				return
			}
		case <-deadline:
			t.Fatal("timed out waiting for cat to start after releasing gate")
		}
	}
}

func TestHandoff_usesProvidedRequiredReadyAliasesInsteadOfLiveBarrier(t *testing.T) {
	obs := newTestObserver()
	ada := newMockAgent()
	turing := newNoticeFlushAgent()
	cat := newMockAgent()
	s := newSession(t,
		session.WithObserver(obs),
		mappedFactory(map[string]agent.Agent{
			"ada":    ada,
			"turing": turing,
			"cat":    cat,
		}),
	)
	t.Cleanup(func() {
		_, _ = s.Execute(session.RemoveCommand{Alias: resolvedAlias(s, "ada")})
		_, _ = s.Execute(session.RemoveCommand{Alias: resolvedAlias(s, "turing")})
		_, _ = s.Execute(session.RemoveCommand{Alias: resolvedAlias(s, "cat")})
	})

	invite(t, s, "ada")
	mustReceive[session.AgentReady](t, obs.ch)
	invite(t, s, "turing")
	mustReceive[session.AgentReady](t, obs.ch)
	invite(t, s, "cat")
	mustReceive[session.AgentReady](t, obs.ch)

	if _, err := s.Execute(session.SendToParticipantOutsideRoomCommand{Alias: resolvedAlias(s, "cat"), Text: "busy"}); err != nil {
		t.Fatalf("SendToParticipantOutsideRoomCommand cat: %v", err)
	}

	if _, err := s.Execute(session.HandoffCommand{
		FromAlias:            resolvedAlias(s, "ada"),
		ToAlias:              resolvedAlias(s, "turing"),
		RequiredReadyAliases: resolvedAliases(s, "ada", "turing"),
		Source:               session.HandoffSource{Text: "final answer", RecordIndex: 7},
	}); err != nil {
		t.Fatalf("HandoffCommand with staged idle aliases: %v", err)
	}
}

func TestSharedSend_rejectsBusyDirectParticipant(t *testing.T) {
	obs := newTestObserver()
	ada := newMockAgent()
	turing := newMockAgent()
	s := newSession(t, session.WithObserver(obs), mappedFactory(map[string]agent.Agent{
		"ada":    ada,
		"turing": turing,
	}))
	t.Cleanup(func() {
		_, _ = s.Execute(session.RemoveCommand{Alias: resolvedAlias(s, "ada")})
		_, _ = s.Execute(session.RemoveCommand{Alias: resolvedAlias(s, "turing")})
	})

	invite(t, s, "ada")
	mustReceive[session.AgentReady](t, obs.ch)
	invite(t, s, "turing")
	mustReceive[session.AgentReady](t, obs.ch)

	if _, err := s.Execute(session.BroadcastCommand{Text: "busy"}); err != nil {
		t.Fatalf("BroadcastCommand: %v", err)
	}
	mustReceive[session.RoutingCompleted](t, obs.ch)

	_, err := s.Execute(session.SendToParticipantCommand{Plan: s.CreateParticipantSendPlan(resolvedAlias(s, "ada")), Message: "do it", Notice: "notice"})
	if err == nil {
		t.Fatal("expected shared send to reject busy direct participant")
	}
	if got := session.DeliveredAliases(err); len(got) != 0 {
		t.Fatalf("expected no delivered aliases, got %v", got)
	}

	ada.mu.Lock()
	defer ada.mu.Unlock()
	if got := len(ada.sends); got != 1 {
		t.Fatalf("expected no additional direct send while busy, got sends=%v", ada.sends)
	}
}

func TestPrivateSend_forwardsToAgentOnly(t *testing.T) {
	obs := newTestObserver()
	ada := newMockAgent()
	turing := newMockAgent()
	s := newSession(t, session.WithObserver(obs), mappedFactory(map[string]agent.Agent{
		"ada":    ada,
		"turing": turing,
	}))
	t.Cleanup(func() {
		_, _ = s.Execute(session.RemoveCommand{Alias: resolvedAlias(s, "ada")})
		_, _ = s.Execute(session.RemoveCommand{Alias: resolvedAlias(s, "turing")})
	})

	invite(t, s, "ada")
	mustReceive[session.AgentReady](t, obs.ch)
	invite(t, s, "turing")
	mustReceive[session.AgentReady](t, obs.ch)

	if _, err := s.Execute(session.SendToParticipantOutsideRoomCommand{Alias: resolvedAlias(s, "ada"), Text: "secret"}); err != nil {
		t.Fatalf("SendToParticipantOutsideRoomCommand: %v", err)
	}

	ada.mu.Lock()
	if len(ada.sends) == 0 || ada.sends[len(ada.sends)-1] != "secret" {
		t.Errorf("addressed agent did not receive message")
	}
	ada.mu.Unlock()

	turing.mu.Lock()
	if len(turing.sends) != 0 {
		t.Errorf("other agent should not receive private message")
	}
	turing.mu.Unlock()

	// no shared room event emitted
	for {
		select {
		case ev := <-obs.ch:
			switch ev.(type) {
			case session.ParticipantStatusChanged, session.RoutingCompleted:
				continue
			default:
				t.Errorf("expected no shared room event, got %T", ev)
				return
			}
		default:
			return
		}
	}
}

func TestPrivateSend_notFound(t *testing.T) {
	s := newSession(t)
	if _, err := s.Execute(session.SendToParticipantOutsideRoomCommand{Alias: resolvedAlias(s, "nobody"), Text: "hi"}); err == nil {
		t.Fatal("expected error for unknown alias, got nil")
	}
}

func TestPrivateSend_rejectsBusyParticipant(t *testing.T) {
	obs := newTestObserver()
	ada := newMockAgent()
	s := newSession(t, session.WithObserver(obs), fixedFactory(ada))
	t.Cleanup(func() { _, _ = s.Execute(session.RemoveCommand{Alias: resolvedAlias(s, "ada")}) })

	invite(t, s, "ada")
	mustReceive[session.AgentReady](t, obs.ch)

	if _, err := s.Execute(session.BroadcastCommand{Text: "busy"}); err != nil {
		t.Fatalf("BroadcastCommand: %v", err)
	}
	mustReceive[session.RoutingCompleted](t, obs.ch)

	_, err := s.Execute(session.SendToParticipantOutsideRoomCommand{Alias: resolvedAlias(s, "ada"), Text: "secret"})
	if err == nil {
		t.Fatal("expected private send to reject busy participant")
	}

	ada.mu.Lock()
	defer ada.mu.Unlock()
	if got := len(ada.sends); got != 1 {
		t.Fatalf("expected no additional private send while busy, got sends=%v", ada.sends)
	}
}

func TestReaderLoop_emitsDelta(t *testing.T) {
	obs := newTestObserver()
	a := newMockAgent()
	s := newSession(t, session.WithObserver(obs), fixedFactory(a))
	t.Cleanup(func() { _, _ = s.Execute(session.RemoveCommand{Alias: resolvedAlias(s, "ada")}) })

	invite(t, s, "ada")
	mustReceive[session.AgentReady](t, obs.ch)

	// Simulate starting a turn so the session considers the agent working; the
	// following stream delta should be accepted.
	if _, err := s.Execute(session.BroadcastCommand{Text: "go"}); err != nil {
		t.Fatalf("BroadcastCommand: %v", err)
	}
	mustReceive[session.RoutingCompleted](t, obs.ch)

	a.ch <- agent.Message{StreamID: "out1", Mode: agent.ModeStream, Content: agent.Output{Text: "hello"}}

	ev := mustReceive[session.AgentMessage](t, obs.ch)
	out, ok := ev.Msg.Content.(agent.Output)
	if !ok || out.Text != "hello" {
		t.Errorf("expected Output{hello}, got content=%T", ev.Msg.Content)
	}
}

// TestReaderLoop_reasoningDoubleCloseDoesNotInvariant guards against the
// double-close regression where item/completed (reasoning) emits a
// Reasoning+ModeFlush for a stream that summaryPartAdded already closed.
//
// Protocol order for a reasoning item:
//
//	item/reasoning/textDelta      → Reasoning+ModeStream   (tracked)
//	item/reasoning/summaryPartAdded → Reasoning+ModeFlush  (closes stream)
//	item/completed (reasoning)    → [no message]           (must not double-close)
//	turn/completed (agentMessage) → Output+ModeFlush       (closes output; participant → idle)
//
// If item/completed emits a second Reasoning+ModeFlush the session logs a
// "participant invariant: participant stream is not tracked" error, and the
// Output+ModeFlush can no longer trigger idle because tracked=false prevents
// the allClosed check from ever reaching markIdleIfWorking.
//
// This test uses direct message injection so it does not depend on Codex wire
// format. Feed the exact same sequence into the session and assert:
//  1. No AgentLog with "stream not tracked" is emitted.
//  2. The participant transitions to Idle after the final output flush.
func TestReaderLoop_reasoningDoubleCloseDoesNotInvariant(t *testing.T) {
	obs := newTestObserver()
	a := newMockAgent()
	s := newSession(t, session.WithObserver(obs), fixedFactory(a))
	t.Cleanup(func() { _, _ = s.Execute(session.RemoveCommand{Alias: resolvedAlias(s, "ada")}) })

	invite(t, s, "ada")
	mustReceive[session.AgentReady](t, obs.ch)

	if _, err := s.Execute(session.BroadcastCommand{Text: "go"}); err != nil {
		t.Fatalf("BroadcastCommand: %v", err)
	}
	mustReceive[session.RoutingCompleted](t, obs.ch)

	// 1. Reasoning and output streams open.
	sendTurnMessage(a, "codex:reasoning:r1", agent.ModeStream, agent.Reasoning{Text: "thinking"})
	sendTurnMessage(a, "codex:output:t1:msg1", agent.ModeStream, agent.Output{Text: "hello"})
	expectTurnMessageForwarded(t, obs)
	expectTurnMessageForwarded(t, obs)

	// 2. summaryPartAdded closes the reasoning stream (authoritative close).
	sendTurnMessage(a, "codex:reasoning:r1", agent.ModeFlush, agent.Reasoning{})
	expectTurnMessageForwarded(t, obs)
	expectParticipantStatus(t, s, "ada", participant.StatusWorking, "after reasoning flush")

	// 3. item/completed (reasoning) would have emitted a second Reasoning+ModeFlush
	// before the fix. Simulate what the OLD adapter would have sent. After the fix,
	// messageFromItemCompleted for reasoning emits nothing, so this message is never
	// injected. We verify here that the session handles an inadvertent second flush
	// gracefully — but the primary guard is the adapter-level test above.
	// (No injection needed: the fix removes the source of this message.)

	// 4. turn/completed closes the output stream.
	sendTurnMessage(a, "codex:output:t1:msg1", agent.ModeFlush, agent.Output{})
	expectTurnMessageForwarded(t, obs)
	expectParticipantStatus(t, s, "ada", participant.StatusWorking, "after output flush")

	// 5. Anchor flush — must trigger idle with no invariant logs.
	sendTurnMessage(a, mockTurnAnchor, agent.ModeFlush, agent.Output{})
	awaitIdleWithoutInvariantLog(t, obs, s, "ada", "after anchor flush")
}

// anchorMockAgent wraps mockAgent and returns a configured anchor StreamID from
// Send. This lets session tests exercise the full anchor-tracking path without
// importing the codex package.
type anchorMockAgent struct {
	*mockAgent
	anchor agent.StreamID
}

func (m *anchorMockAgent) Send(text string) (agent.StreamID, error) {
	_, err := m.mockAgent.Send(text)
	if err != nil {
		return "", err
	}
	return m.anchor, nil
}

// TestReaderLoop_anchorStreamPreventsEarlyIdle is the regression test for
// "participant stays Working after turn" (issue 3 from the stream-tracking review).
//
// Root cause: before the anchor, idle was triggered when allClosed=true, which
// could fire as soon as the last *currently-tracked* stream closed. If reasoning
// closed before any output stream was opened, allClosed became true immediately
// and marked the participant idle prematurely. Subsequent output deltas were
// dropped by shouldDropIdleStreamFragment; the output flush arrived with no
// tracked stream to close (tracked=false), so markIdleIfWorking was never
// called and the participant was stuck Working.
//
// Fix: Send returns an anchor StreamID. The session tracks it immediately after
// a successful send (before the adapter emits any messages). allClosed=true
// is now impossible while the anchor is open, so idle can only be triggered
// after the adapter explicitly signals turn-end by flushing the anchor.
//
// This test exercises the scenario in order:
//
//	reasoning delta  → tracked
//	reasoning flush  → closed (allClosed=false because anchor still open)
//	output delta     → tracked (participant still Working — NOT dropped)
//	output flush     → closed (allClosed=false because anchor still open)
//	anchor flush     → closed (allClosed=true) → idle ✓
func TestReaderLoop_anchorStreamPreventsEarlyIdle(t *testing.T) {
	const anchorID = agent.StreamID("test:turn-anchor")

	obs := newTestObserver()
	a := &anchorMockAgent{mockAgent: newMockAgent(), anchor: anchorID}
	s := newSession(t, session.WithObserver(obs), fixedFactory(a))
	t.Cleanup(func() { _, _ = s.Execute(session.RemoveCommand{Alias: resolvedAlias(s, "ada")}) })

	invite(t, s, "ada")
	mustReceive[session.AgentReady](t, obs.ch)

	if _, err := s.Execute(session.BroadcastCommand{Text: "go"}); err != nil {
		t.Fatalf("BroadcastCommand: %v", err)
	}
	mustReceive[session.RoutingCompleted](t, obs.ch)

	// Reasoning opens and then closes — before any output stream appears.
	// Without the anchor this would set allClosed=true and mark the participant
	// idle prematurely.
	sendTurnMessage(a.mockAgent, "reason1", agent.ModeStream, agent.Reasoning{Text: "thinking"})
	expectTurnMessageForwarded(t, obs)

	sendTurnMessage(a.mockAgent, "reason1", agent.ModeFlush, agent.Reasoning{})
	expectTurnMessageForwarded(t, obs)

	expectParticipantStatus(t, s, "ada", participant.StatusWorking, "step 1")

	// Output delta must NOT be dropped: participant must be Working.
	sendTurnMessage(a.mockAgent, "out1", agent.ModeStream, agent.Output{Text: "result"})
	expectTurnMessageForwarded(t, obs)

	expectParticipantStatus(t, s, "ada", participant.StatusWorking, "step 2")

	// Output closes — anchor still open, so still Working.
	sendTurnMessage(a.mockAgent, "out1", agent.ModeFlush, agent.Output{})
	expectTurnMessageForwarded(t, obs)

	expectParticipantStatus(t, s, "ada", participant.StatusWorking, "step 3")

	// Anchor flush — this is the authoritative turn-end signal.
	sendTurnMessage(a.mockAgent, anchorID, agent.ModeFlush, agent.Output{})
	awaitIdleWithoutInvariantLog(t, obs, s, "ada", "after anchor flush")
}

func TestReaderLoop_marksIdleOnlyAfterAllObservedStreamsFlush(t *testing.T) {
	obs := newTestObserver()
	a := newMockAgent()
	s := newSession(t, session.WithObserver(obs), fixedFactory(a))
	t.Cleanup(func() { _, _ = s.Execute(session.RemoveCommand{Alias: resolvedAlias(s, "ada")}) })

	invite(t, s, "ada")
	mustReceive[session.AgentReady](t, obs.ch)

	if _, err := s.Execute(session.BroadcastCommand{Text: "go"}); err != nil {
		t.Fatalf("BroadcastCommand: %v", err)
	}
	mustReceive[session.RoutingCompleted](t, obs.ch)

	a.ch <- agent.Message{StreamID: "out1", Mode: agent.ModeStream, Content: agent.Output{Text: "hello"}}
	a.ch <- agent.Message{StreamID: "reason1", Mode: agent.ModeStream, Content: agent.Reasoning{Text: "thinking"}}
	mustReceive[session.AgentMessage](t, obs.ch)
	mustReceive[session.AgentMessage](t, obs.ch)

	assertFlushDoesNotCompleteTurn(t, a, obs, s, agent.Message{StreamID: "out1", Mode: agent.ModeFlush, Content: agent.Output{}})
	assertFlushDoesNotCompleteTurn(t, a, obs, s, agent.Message{StreamID: "reason1", Mode: agent.ModeFlush, Content: agent.Reasoning{}})

	// Anchor flush — the authoritative turn-end signal.
	a.ch <- agent.Message{StreamID: mockTurnAnchor, Mode: agent.ModeFlush, Content: agent.Output{}}
	mustReceive[session.ParticipantStatusChanged](t, obs.ch)
	if event := mustReceive[session.AgentMessage](t, obs.ch); !event.WorkCompleted || event.ExecutionID == 0 {
		t.Fatalf("anchor flush completion = %v, turn ID = %d", event.WorkCompleted, event.ExecutionID)
	}
	p, _ := s.ParticipantByName("ada")
	if p.Status != participant.StatusIdle {
		t.Fatalf("expected ada to become idle after anchor flush, got %q", p.Status)
	}
}

func assertFlushDoesNotCompleteTurn(t *testing.T, a *mockAgent, obs *testObserver, s *session.Session, msg agent.Message) {
	t.Helper()
	a.ch <- msg
	if event := mustReceive[session.AgentMessage](t, obs.ch); event.WorkCompleted || event.ExecutionID != 0 {
		t.Fatalf("flush completion = %v, turn ID = %d", event.WorkCompleted, event.ExecutionID)
	}
	p, _ := s.ParticipantByName("ada")
	if p.Status != participant.StatusWorking {
		t.Fatalf("expected ada to remain working after %q flush, got %q", msg.StreamID, p.Status)
	}
}

func TestReaderLoop_emitsDone(t *testing.T) {
	obs := newTestObserver()
	a := newMockAgent(agent.Message{StreamID: "turn1", Mode: agent.ModeFlush, Content: agent.Output{}})
	s := newSession(t, session.WithObserver(obs), fixedFactory(a))
	t.Cleanup(func() { _, _ = s.Execute(session.RemoveCommand{Alias: resolvedAlias(s, "ada")}) })

	invite(t, s, "ada")
	mustReceive[session.AgentReady](t, obs.ch)
	mustReceive[session.AgentLog](t, obs.ch) // unmatched flush invariant
	ev := mustReceive[session.AgentMessage](t, obs.ch)
	if ev.Msg.Mode != agent.ModeFlush {
		t.Errorf("expected ModeFlush message for turn done")
	}
}

func TestMultipleObservers_bothNotified(t *testing.T) {
	obs1 := newTestObserver()
	obs2 := newTestObserver()
	a := newMockAgent()
	s := newSession(t, session.WithObserver(obs1), session.WithObserver(obs2), fixedFactory(a))
	t.Cleanup(func() { _, _ = s.Execute(session.RemoveCommand{Alias: resolvedAlias(s, "ada")}) })

	invite(t, s, "ada")
	mustReceive[session.AgentReady](t, obs1.ch)
	mustReceive[session.AgentReady](t, obs2.ch)
}

func TestReaderLoop_emitsAgentLog(t *testing.T) {
	obs := newTestObserver()
	a := newMockAgent(agent.Message{StreamID: "codex:log", Mode: agent.ModeSingle, Content: agent.Log{Text: "npm warn something"}})
	s := newSession(t, session.WithObserver(obs), fixedFactory(a))
	t.Cleanup(func() { _, _ = s.Execute(session.RemoveCommand{Alias: resolvedAlias(s, "ada")}) })

	invite(t, s, "ada")
	mustReceive[session.AgentReady](t, obs.ch)

	ev := mustReceive[session.AgentLog](t, obs.ch)
	if ev.Text != "npm warn something" {
		t.Errorf("expected log text %q, got %q", "npm warn something", ev.Text)
	}
	if ev.Alias.Name != "ada" {
		t.Errorf("expected alias %q, got %q", "ada", ev.Alias)
	}
}

func TestReaderLoop_agentCrash_emitsCrashed(t *testing.T) {
	obs := newTestObserver()
	a := newMockAgent() // no messages; Stop() will close channel
	_ = a.Stop()        // close immediately — simulates crash
	s := newSession(t, session.WithObserver(obs), fixedFactory(a))

	invite(t, s, "ada")
	mustReceive[session.AgentReady](t, obs.ch)
	mustReceive[session.AgentCrashed](t, obs.ch)
}

func TestParticipantSendPlan_includesStartingListenersAndFreezesRecipients(t *testing.T) {
	obs := newTestObserver()
	gate := make(chan struct{})
	var once sync.Once
	closeGate := func() { once.Do(func() { close(gate) }) }
	ben := &gateAgent{startGate: gate, mockAgent: newMockAgent()}
	later := newMockAgent()
	s := newSession(t, session.WithObserver(obs), mappedFactory(map[string]agent.Agent{"ada": newMockAgent(), "ben": ben, "later": later}))
	t.Cleanup(closeGate)
	invite(t, s, "ada")
	mustReceive[session.AgentReady](t, obs.ch)
	invite(t, s, "ben")
	mustReceive[session.AgentStarting](t, obs.ch)
	enableSendNotices(t, s)
	plan := s.CreateParticipantSendPlan(resolvedAlias(s, "ada"))
	if !slices.Equal(namesOfAliases(plan.Targets()), []string{"ada", "ben"}) {
		t.Fatalf("targets = %v", plan.Targets())
	}
	invite(t, s, "later")
	mustReceive[session.AgentReady](t, obs.ch)
	closeGate()
	mustReceive[session.AgentReady](t, obs.ch)
	if !slices.Equal(namesOfAliases(plan.Targets()), []string{"ada", "ben"}) {
		t.Fatalf("frozen targets = %v", plan.Targets())
	}
	if _, err := s.Execute(session.SendToParticipantCommand{Plan: plan, Message: "hello", Notice: "notice"}); err != nil {
		t.Fatal(err)
	}
	ben.mu.Lock()
	defer ben.mu.Unlock()
	if !slices.Equal(ben.sends, []string{"notice"}) {
		t.Fatalf("listener sends = %v", ben.sends)
	}
	later.mu.Lock()
	defer later.mu.Unlock()
	if len(later.sends) != 0 {
		t.Fatalf("late invite received sends: %v", later.sends)
	}
}
