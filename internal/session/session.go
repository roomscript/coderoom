// Package session implements the Session Controller: the central orchestrator
// for command dispatch, message routing, and participant lifecycle.
// See docs/design/pkg-session.md for the full design rationale.
package session

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/trigosec/coderoom/internal/agent"
	roomconfig "github.com/trigosec/coderoom/internal/config"
	"github.com/trigosec/coderoom/internal/participant"
	"github.com/trigosec/coderoom/internal/policy"
)

// AgentFactory constructs the adapter selected by the session for a participant
// config. The application composition root maps backend values to concrete
// packages and wires session-owned facilities before returning.
//
// The session is passed to allow factories to use session-owned facilities
// (e.g., approval routing) without requiring UI-owned glue code.
type AgentFactory func(s *Session, cfg roomconfig.ParticipantConfig, backend AgentBackend) agent.Agent

// AgentBackend identifies the adapter implementation requested by room policy.
type AgentBackend string

const (
	// AgentBackendDefault requests the application's ordinary agent adapter.
	AgentBackendDefault AgentBackend = "default"
	// AgentBackendEcho requests the deterministic echo adapter.
	AgentBackendEcho AgentBackend = "echo"
)

type agentRuntime struct {
	agentCancel context.CancelFunc
	stop        chan struct{}
}

// Session is the central orchestrator of a coderoom session.
// Execute must be called from a single goroutine (the TUI input loop).
// It is not safe for concurrent calls to Execute.
type Session struct {
	mu            sync.Mutex
	registry      *participant.Registry
	agents        map[string]agentRuntime
	obs           []Observer
	now           func() time.Time
	keepaliveTick time.Duration
	agentFactory  AgentFactory
	config        *roomconfig.Config
	policies      policy.Set
	hasInvited    bool
	approvals     *approvalHub
	lastTurnID    uint64
	lifecycle     sessionLifecycle
}

type sessionLifecycle struct {
	ctx          context.Context
	cancelFn     context.CancelFunc
	keepaliveWG  sync.WaitGroup
	stopOnce     sync.Once
	shutdownOnce sync.Once
}

var errParticipantNotFound = errors.New("participant not found")

// Option configures a Session at construction time.
type Option func(*Session)

// WithContext sets the parent context for session-owned background work such as
// the keepalive ticker.
func WithContext(ctx context.Context) Option {
	return func(s *Session) {
		if ctx == nil {
			return
		}
		if s.lifecycle.cancelFn != nil {
			s.lifecycle.cancelFn()
		}
		s.lifecycle.ctx, s.lifecycle.cancelFn = context.WithCancel(ctx) //nolint:gosec // cancel stored and invoked by Shutdown
	}
}

// WithObserver appends an Observer that receives all session events.
// May be called multiple times to register multiple observers.
func WithObserver(obs Observer) Option {
	return func(s *Session) { s.obs = append(s.obs, obs) }
}

// AddObserver appends an Observer after construction.
func (s *Session) AddObserver(obs Observer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.obs = append(s.obs, obs)
}

// WithAgentFactory sets the factory used to construct agents when a participant
// is invited. The factory receives the resolved participant config and is
// responsible for wiring all backend options using session-owned facilities
// where applicable.
func WithAgentFactory(f AgentFactory) Option {
	return func(s *Session) { s.agentFactory = f }
}

// WithConfig sets the repo-local configuration resolver used during invite.
func WithConfig(cfg *roomconfig.Config) Option {
	return func(s *Session) { s.config = cfg }
}

// New returns an empty Session.
func New(opts ...Option) *Session {
	s := &Session{
		registry:      participant.NewRegistry(),
		agents:        make(map[string]agentRuntime),
		now:           time.Now,
		keepaliveTick: 30 * time.Second,
		policies:      policy.NewSet(),
	}
	s.lifecycle.ctx, s.lifecycle.cancelFn = context.WithCancel(context.Background()) //nolint:gosec // cancel stored and invoked by Shutdown
	s.approvals = newApprovalHub(s.notify)
	for _, o := range opts {
		o(s)
	}
	s.startKeepaliveLoop()
	return s
}

// ApprovalListener returns an ApprovalListener that publishes approval requests
// as session events and blocks until the session resolves them.
func (s *Session) ApprovalListener(alias string) agent.ApprovalListener {
	return s.approvals.Listener(alias)
}

// CreateAgentRuntime reserves the session-owned runtime record for alias.
func (s *Session) CreateAgentRuntime(alias string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.agents[alias]; ok {
		return
	}
	s.agents[alias] = agentRuntime{}
}

// CreateAgentContext derives a per-agent lifecycle context from the session
// lifecycle context and records it in the agent runtime under alias.
func (s *Session) CreateAgentContext(alias string) context.Context {
	s.mu.Lock()
	defer s.mu.Unlock()
	runtime := s.agents[alias]
	if runtime.agentCancel != nil {
		runtime.agentCancel()
	}
	ctx, cancel := context.WithCancel(s.lifecycle.ctx) //nolint:gosec // cancel stored in agent runtime and invoked by session lifecycle
	runtime.agentCancel = cancel
	s.agents[alias] = runtime
	return ctx
}

// Execute dispatches a command. Must be called from a single goroutine.
func (s *Session) Execute(cmd Command) error {
	return cmd.execute(s)
}

func (s *Session) snapshotAgentsToStop() []agent.Agent {
	s.mu.Lock()
	defer s.mu.Unlock()
	ps := s.registry.List()
	out := make([]agent.Agent, 0, len(ps))
	for _, p := range ps {
		if p.Agent == nil {
			continue
		}
		out = append(out, p.Agent)
	}
	return out
}

// Shutdown stops all agents in the session. Repeated calls are safe. Errors
// from individual agents are silently discarded; the goal is best-effort
// cleanup on process exit.
func (s *Session) Shutdown() {
	s.lifecycle.shutdownOnce.Do(func() {
		s.stopBackgroundLoops()
		s.cancelAllAgentContexts()
		// Note: participants in StatusStarting do not have Agent set yet (it is
		// bound by AttachAgent when they transition to StatusAttached). Those
		// in-flight processes are not stoppable via Session.Shutdown.
		for _, a := range s.snapshotAgentsToStop() {
			_ = a.Stop()
		}
	})
}

func (s *Session) stopBackgroundLoops() {
	s.lifecycle.stopOnce.Do(func() {
		if s.lifecycle.cancelFn != nil {
			s.lifecycle.cancelFn()
			s.lifecycle.cancelFn = nil
		}
		s.lifecycle.keepaliveWG.Wait()
	})
}

func (s *Session) cancelAllAgentContexts() {
	s.mu.Lock()
	runtimes := make([]agentRuntime, 0, len(s.agents))
	for _, runtime := range s.agents {
		runtimes = append(runtimes, runtime)
	}
	s.agents = make(map[string]agentRuntime)
	s.mu.Unlock()
	for _, runtime := range runtimes {
		if runtime.agentCancel != nil {
			runtime.agentCancel()
		}
	}
}

func (s *Session) cancelAgentContext(alias string) {
	s.mu.Lock()
	runtime, ok := s.agents[alias]
	if ok {
		delete(s.agents, alias)
	}
	s.mu.Unlock()
	if ok && runtime.agentCancel != nil {
		runtime.agentCancel()
	}
}

func (s *Session) notify(e Event) {
	s.mu.Lock()
	obs := s.obs
	s.mu.Unlock()
	for _, o := range obs {
		o.OnEvent(e)
	}
}

func (s *Session) notifyParticipantInvariant(alias string, err error, details ...string) {
	if err == nil {
		return
	}
	text := "participant invariant: " + err.Error()
	if len(details) > 0 {
		cp := append([]string(nil), details...)
		filtered := cp[:0]
		for i := range cp {
			item := strings.TrimSpace(cp[i])
			if item == "" {
				continue
			}
			filtered = append(filtered, item)
		}
		if len(filtered) > 0 {
			text += " (" + strings.Join(filtered, "; ") + ")"
		}
	}
	s.notify(AgentLog{Alias: alias, Text: text})
}

func summarizeAgentMessage(msg agent.Message) string {
	var payload string
	switch c := msg.Content.(type) {
	case agent.Output:
		payload = "output=" + truncateForLog(c.Text)
	case agent.Reasoning:
		payload = "reasoning=" + truncateForLog(c.Text)
	case agent.Command:
		if c.Command != "" {
			payload = "command=" + truncateForLog(c.Command)
		} else if c.Output != "" {
			payload = "command_output=" + truncateForLog(c.Output)
		}
	case agent.FileChangeSet:
		payload = fmt.Sprintf("file_changes=%d status=%q", len(c.Changes), c.Status)
	default:
		// agent.Log is handled earlier; keep the rest type-oriented.
		payload = fmt.Sprintf("content=%T", msg.Content)
	}
	if payload == "" {
		payload = fmt.Sprintf("content=%T", msg.Content)
	}
	return fmt.Sprintf("mode=%s stream=%q %s", agentModeString(msg.Mode), msg.StreamID, payload)
}

func agentModeString(m agent.Mode) string {
	switch m {
	case agent.ModeStream:
		return "stream"
	case agent.ModeFlush:
		return "flush"
	case agent.ModeSingle:
		return "single"
	default:
		return fmt.Sprintf("unknown(%d)", m)
	}
}

func truncateForLog(s string) string {
	const maxLen = 200
	if s == "" {
		return ""
	}
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "…"
}

func (s *Session) addParticipant(p *participant.Participant) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.registry.Add(p); err != nil {
		return fmt.Errorf("register participant: %w", err)
	}
	return nil
}

// detachParticipant removes the participant and its attached runtime atomically,
// closes the stop channel (signals readLoop: stopped, not crashed), and returns
// the participant so the caller can stop the agent.
//
// Return values:
//   - (p, true)   — detached successfully; p is the participant (may be nil on
//     registry inconsistency, which the caller handles as best-effort cleanup).
//   - (p, false)  — participant exists but IsRemovable is false (startup window);
//     caller should surface "not ready" rather than "not found".
//   - (nil, false) — no attached runtime; caller falls through to evictCrashedBeforeStart.
func (s *Session) detachParticipant(alias string) (*participant.Participant, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	runtime, ok := s.agents[alias]
	if !ok || runtime.stop == nil {
		return nil, false
	}
	p, _ := s.registry.Get(alias)
	// Bound-runtime removal still waits for startup publication. Failed startup
	// without a reader is handled by evictCrashedBeforeStart instead.
	if p != nil && (!p.StartupReady || !p.IsRemovable()) {
		return p, false
	}
	delete(s.agents, alias)
	if runtime.agentCancel != nil {
		runtime.agentCancel()
	}
	_ = s.registry.Remove(alias)
	close(runtime.stop)
	return p, true
}

// evictCrashedBeforeStart removes a participant that crashed before its reader
// was started (runtime exists but has no stop channel yet, or runtime is gone).
// It checks status atomically under the session lock so that a concurrently
// completing startInvitedAgent goroutine cannot be mistakenly evicted.
//
// Returns "not found" for unknown aliases, "not ready" for participants still
// in the startup window (StatusStarting), and nil on successful eviction of a
// StatusCrashed participant.
func (s *Session) evictCrashedBeforeStart(alias string) error {
	s.mu.Lock()
	p, ok := s.registry.Get(alias)
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("participant %q not found", alias)
	}
	if p.Status != participant.StatusCrashed || !p.IsRemovable() {
		s.mu.Unlock()
		return fmt.Errorf("participant %q is not ready", alias)
	}
	if runtime, ok := s.agents[alias]; ok {
		if runtime.agentCancel != nil {
			runtime.agentCancel()
		}
		delete(s.agents, alias)
	}
	_ = s.registry.Remove(alias)
	s.mu.Unlock()
	s.notify(AgentStopped{Alias: alias})
	return nil
}

// attachParticipant transitions the participant to StatusAttached (binding the
// agent) and completes the runtime with its stop channel under one lock
// acquisition, so /remove cannot observe a window where the runtime is fully
// attached but the participant is still StatusStarting.
//
// The caller is responsible for starting the read goroutine after the full
// startup sequence (AgentStarted + commitStarted) has completed, so that
// early agent output is never processed while the participant is still Attached.
//
// Returns (stop, from, true) on success, where from is the pre-transition
// status (StatusStarting) used to build the ParticipantStatusChanged event.
func (s *Session) attachParticipant(alias string, a agent.Agent) (chan struct{}, participant.Status, bool) {
	stop := make(chan struct{})
	s.mu.Lock()
	p, ok := s.registry.Get(alias)
	if !ok {
		s.mu.Unlock()
		return nil, "", false
	}
	from := p.Status
	if err := p.AttachAgent(a, s.now()); err != nil {
		s.mu.Unlock()
		s.notifyParticipantInvariant(alias, fmt.Errorf("attach agent: %w", err))
		return nil, "", false
	}
	runtime, ok := s.agents[alias]
	if !ok {
		s.mu.Unlock()
		s.notifyParticipantInvariant(alias, fmt.Errorf("attach agent: missing runtime"))
		return nil, "", false
	}
	runtime.stop = stop
	s.agents[alias] = runtime
	s.mu.Unlock()
	return stop, from, true
}

// commitStarted transitions the participant from StatusAttached to StatusIdle
// and returns the corresponding ParticipantStatusChanged event. Called before
// AgentStarted is dispatched so that IsSendable is true when the event fires.
func (s *Session) commitStarted(alias string, from participant.Status) (Event, bool) {
	s.mu.Lock()
	p, ok := s.registry.Get(alias)
	if !ok {
		s.mu.Unlock()
		return nil, false
	}
	if err := p.CommitIdle(s.now()); err != nil {
		s.mu.Unlock()
		s.notifyParticipantInvariant(alias, fmt.Errorf("commit idle: %w", err))
		return nil, false
	}
	ev := Event(ParticipantStatusChanged{Alias: alias, From: from, To: p.Status, Since: p.Since})
	s.mu.Unlock()
	return ev, true
}

func (s *Session) lookupParticipant(alias string) (*participant.Participant, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.registry.Get(alias)
}

// readParticipantRuntime captures transport handles for session-internal
// delivery. Public queries expose only View and cannot invoke the backend.
func (s *Session) readParticipantRuntime(alias string) (participant.Participant, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.registry.Get(alias)
	if !ok {
		return participant.Participant{}, false
	}
	return p.Snapshot(), true
}

func (s *Session) updateParticipant(alias string, fn func(*participant.Participant) (Event, error)) error {
	var ev Event
	var err error
	s.mu.Lock()
	p, ok := s.registry.Get(alias)
	if ok {
		ev, err = fn(p)
	}
	s.mu.Unlock()
	if !ok {
		return fmt.Errorf("%w: %q", errParticipantNotFound, alias)
	}
	if err != nil {
		return err
	}
	if ev != nil {
		s.notify(ev)
	}
	return nil
}

// Participant returns a detached observable view of one registered participant.
// Runtime handles, streams, and mutation methods remain private to the session.
func (s *Session) Participant(alias string) (participant.View, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.registry.Get(alias)
	if !ok {
		return participant.View{}, false
	}
	return p.View, true
}

// Participants returns all registered participants in one locked snapshot,
// including startup and crashed states. Status and readiness are not rewritten
// or filtered; consumers select recipients using the shared View predicates.
func (s *Session) Participants() []participant.View {
	s.mu.Lock()
	defer s.mu.Unlock()
	ps := s.registry.List()
	out := make([]participant.View, len(ps))
	for i, p := range ps {
		out[i] = p.View
	}
	return out
}

// HasAnyActivityParticipants reports whether any participant is in a status
// that requires the activity monitor to tick.
func (s *Session) HasAnyActivityParticipants() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.registry.HasStarting() || s.registry.HasAttached() || s.registry.HasKeepalive() || s.registry.HasWorking() || s.registry.HasCrashed()
}

// readLoop runs in a goroutine per agent, forwarding agent.Message values to
// the observers. It is started after commitStarted and SessionReady so the
// participant is StatusIdle when the first message arrives, but before
// AgentStarted is dispatched so the agent pipe is drained immediately and
// cannot stall the child process while observers are being notified.
func (s *Session) readLoop(stop <-chan struct{}, alias string, a agent.Agent) {
	for {
		msg, err := a.Read()
		if err != nil {
			s.handleAgentReadError(stop, alias)
			return
		}
		s.handleAgentMessage(alias, msg)
	}
}

func (s *Session) handleAgentReadError(stop <-chan struct{}, alias string) {
	if isCrashedReadError(stop) {
		err := s.updateParticipant(alias, func(p *participant.Participant) (Event, error) {
			from := p.Status
			p.Crash(s.now())
			return ParticipantStatusChanged{Alias: alias, From: from, To: p.Status, Since: p.Since}, nil
		})
		if err != nil && !errors.Is(err, errParticipantNotFound) {
			s.notifyParticipantInvariant(alias, err)
		}
	}
	s.notify(readErrorEvent(stop, alias))
}

func readErrorEvent(stop <-chan struct{}, alias string) Event {
	if isCrashedReadError(stop) {
		return AgentCrashed{Alias: alias}
	}
	return AgentStopped{Alias: alias}
}

func isCrashedReadError(stop <-chan struct{}) bool {
	select {
	case <-stop:
		return false
	default:
		return true
	}
}

// prepareParticipantForWork transitions the participant to Preparing state
// under the session lock. Must be followed by Send then beginParticipantWorking
// (on success) or abortWork (on Send failure).
func (s *Session) prepareParticipantForWork(alias string) error {
	var ev ParticipantStatusChanged
	s.mu.Lock()
	p, ok := s.registry.Get(alias)
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("%w: %q", errParticipantNotFound, alias)
	}
	from := p.Status
	if err := p.PrepareForWork(s.now()); err != nil {
		s.mu.Unlock()
		return fmt.Errorf("prepare for work: %w", err)
	}
	ev = ParticipantStatusChanged{Alias: alias, From: from, To: p.Status, Since: p.Since}
	s.mu.Unlock()
	s.notify(ev)
	return nil
}

// beginParticipantWorking transitions from Preparing to Working and atomically
// tracks the turn-lifecycle anchor in OpenStreams. Call this after a successful
// Send to close the race window between PrepareForWork and anchor tracking.
func (s *Session) beginParticipantWorking(alias string, anchor agent.StreamID) {
	err := s.updateParticipant(alias, func(p *participant.Participant) (Event, error) {
		from := p.Status
		turnID := s.lastTurnID + 1
		if err := p.BeginWorking(s.now(), anchor, turnID); err != nil {
			return nil, fmt.Errorf("begin working: %w", err)
		}
		s.lastTurnID = turnID
		return ParticipantStatusChanged{Alias: alias, From: from, To: p.Status, Since: p.Since}, nil
	})
	if err != nil && !errors.Is(err, errParticipantNotFound) {
		s.notifyParticipantInvariant(alias, err)
	}
}

// abortWork rolls back a Preparing or Working participant to Idle. Use when
// Send fails after prepareParticipantForWork, or for error rollback paths.
func (s *Session) abortWork(alias string) {
	err := s.updateParticipant(alias, func(p *participant.Participant) (Event, error) {
		if p.Status != participant.StatusWorking && p.Status != participant.StatusPreparing {
			return nil, nil
		}
		from := p.Status
		if err := p.AbortWork(s.now()); err != nil {
			return nil, fmt.Errorf("abort work: %w", err)
		}
		return ParticipantStatusChanged{Alias: alias, From: from, To: p.Status, Since: p.Since}, nil
	})
	if err != nil && !errors.Is(err, errParticipantNotFound) {
		s.notifyParticipantInvariant(alias, err)
	}
}

// markIdle transitions the participant to Idle via BecomeIdle. Called when the
// anchor stream flush is received (shouldIdle=true from CloseStream).
func (s *Session) markIdle(alias string) (uint64, bool) {
	var turnID uint64
	err := s.updateParticipant(alias, func(p *participant.Participant) (Event, error) {
		from := p.Status
		turnID = p.TurnID
		if err := p.BecomeIdle(s.now()); err != nil {
			return nil, fmt.Errorf("become idle: %w", err)
		}
		return ParticipantStatusChanged{Alias: alias, From: from, To: p.Status, Since: p.Since}, nil
	})
	if err != nil && !errors.Is(err, errParticipantNotFound) {
		s.notifyParticipantInvariant(alias, err)
	}
	return turnID, err == nil
}

// trackAnchorStream tracks a stream for a participant that is already Working
// (e.g., a listener receiving a notice while an existing turn is in flight).
// Unlike beginParticipantWorking, this does not change the participant's status
// or replace its existing anchor.
func (s *Session) trackAnchorStream(alias string, streamID agent.StreamID) {
	if streamID == "" {
		return
	}
	s.mu.Lock()
	p, ok := s.registry.Get(alias)
	var err error
	if ok {
		err = p.TrackStream(streamID)
	}
	s.mu.Unlock()
	if err != nil {
		s.notifyParticipantInvariant(alias, err)
	}
}

func (s *Session) noteWorkingStreamMessage(alias string, msg agent.Message) (shouldIdle bool, sawTracked bool) {
	var err error
	s.mu.Lock()
	p, ok := s.registry.Get(alias)
	if !ok {
		s.mu.Unlock()
		return false, false
	}
	switch msg.Mode {
	case agent.ModeStream:
		err = p.TrackStream(msg.StreamID)
		s.mu.Unlock()
		if err != nil {
			s.notifyParticipantInvariant(alias, err, summarizeAgentMessage(msg))
			return false, false
		}
		return false, true
	case agent.ModeFlush:
		shouldIdle, err = p.CloseStream(msg.StreamID)
		s.mu.Unlock()
		if err != nil {
			// ErrStreamNotTracked on a flush is expected when belt-and-suspenders
			// close signals overlap (e.g. item/completed and turn/completed both
			// carry a close for the same stream). Silence it; other errors are
			// genuine invariant violations.
			if !errors.Is(err, participant.ErrStreamNotTracked) {
				s.notifyParticipantInvariant(alias, err, summarizeAgentMessage(msg))
			}
			return false, false
		}
		return shouldIdle, true
	default:
		s.mu.Unlock()
		return false, false
	}
}

func (s *Session) participantStatus(alias string) (participant.Status, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.registry.Get(alias)
	if !ok {
		return "", false
	}
	return p.Status, true
}

func (s *Session) handleAgentMessage(alias string, msg agent.Message) {
	if s.shouldDropIdleStreamFragment(alias, msg) {
		return
	}

	if _, ok := msg.Content.(agent.KeepAlive); ok {
		s.finishParticipantKeepalive(alias)
		return
	}

	if c, ok := msg.Content.(agent.Log); ok {
		if c.Text != "" {
			s.notify(AgentLog{Alias: alias, Text: c.Text})
		}
		return
	}

	turnID, turnCompleted := s.applyTurnLifecycle(alias, msg)
	m := msg
	s.notify(AgentMessage{
		Alias:         alias,
		Msg:           m,
		TurnCompleted: turnCompleted,
		TurnID:        turnID,
	})
}

func (s *Session) finishParticipantKeepalive(alias string) {
	err := s.updateParticipant(alias, func(p *participant.Participant) (Event, error) {
		if p.Status != participant.StatusKeepalive {
			return nil, nil
		}
		from := p.Status
		if err := p.FinishKeepalive(s.now()); err != nil {
			return nil, fmt.Errorf("finish keepalive: %w", err)
		}
		return ParticipantStatusChanged{Alias: alias, From: from, To: p.Status, Since: p.Since}, nil
	})
	if err != nil && !errors.Is(err, errParticipantNotFound) {
		s.notifyParticipantInvariant(alias, err)
	}
}

func (s *Session) applyTurnLifecycle(alias string, msg agent.Message) (uint64, bool) {
	// Stream tracking only — no status transitions here. Transitions happen via
	// prepareParticipantForWork / beginParticipantWorking / markIdle driven by
	// the session command layer and the anchor stream close.
	switch msg.Content.(type) {
	case agent.Output, agent.Reasoning, agent.Command, agent.FileChangeSet:
		switch msg.Mode {
		case agent.ModeStream:
			_, _ = s.noteWorkingStreamMessage(alias, msg)
		case agent.ModeFlush:
			if shouldIdle, tracked := s.noteWorkingStreamMessage(alias, msg); tracked && shouldIdle {
				return s.markIdle(alias)
			}
		case agent.ModeSingle:
			// Standalone messages are not stream-tracked.
		}
	}
	return 0, false
}

func (s *Session) shouldDropIdleStreamFragment(alias string, msg agent.Message) bool {
	// Protocol guard:
	// Streaming fragments must belong to an active turn. If an adapter emits one
	// while the participant is idle, it is a lifecycle violation. Drop it to
	// avoid spuriously flipping the participant back to working and confusing
	// barrier-based UI features.
	//
	// Note: we allow agent.Log unconditionally (it is not turn-scoped).
	if msg.Mode != agent.ModeStream {
		return false
	}
	st, ok := s.participantStatus(alias)
	if !ok || st != participant.StatusIdle {
		return false
	}
	switch msg.Content.(type) {
	case agent.Output, agent.Reasoning, agent.Command, agent.FileChangeSet:
		s.notify(AgentLog{
			Alias: alias,
			Text:  "protocol: received stream fragment while idle; dropping (try cancel to resync if the agent is stuck; " + summarizeAgentMessage(msg) + ")",
		})
		return true
	default:
		return false
	}
}

type keepaliveCandidate struct {
	alias string
	agent agent.Keepaliver
}

func (s *Session) runKeepalive(candidate keepaliveCandidate) {
	if err := candidate.agent.KeepAlive(); err != nil {
		s.notify(AgentLog{Alias: candidate.alias, Text: fmt.Sprintf("keepalive failed: %v", err)})
		s.finishParticipantKeepalive(candidate.alias)
	}
}

func (s *Session) startKeepaliveLoop() {
	if s.keepaliveTick <= 0 {
		return
	}
	s.lifecycle.keepaliveWG.Add(1)
	go func() {
		defer s.lifecycle.keepaliveWG.Done()
		s.keepaliveLoop(s.lifecycle.ctx, s.keepaliveTick)
	}()
}

func (s *Session) keepaliveLoop(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.sweepKeepalive()
		case <-ctx.Done():
			return
		}
	}
}

func (s *Session) sweepKeepalive() {
	now := s.now()
	candidates, events := s.collectKeepaliveCandidates(now)
	for _, ev := range events {
		s.notify(ev)
	}
	for _, candidate := range candidates {
		go s.runKeepalive(candidate)
	}
}

func (s *Session) collectKeepaliveCandidates(now time.Time) ([]keepaliveCandidate, []Event) {
	var candidates []keepaliveCandidate
	var events []Event
	s.mu.Lock()
	for _, p := range s.registry.List() {
		alias := p.Alias
		if p.Status != participant.StatusIdle || p.Agent == nil {
			continue
		}
		if _, ok := s.agents[alias]; !ok {
			continue
		}
		keepaliver, ok := p.Agent.(agent.Keepaliver)
		if !ok {
			continue
		}
		wait := keepaliver.KeepAliveSchedule()
		if wait <= 0 || now.Sub(p.Since) < wait {
			continue
		}
		from := p.Status
		if err := p.BeginKeepalive(now); err != nil {
			continue
		}
		events = append(events, ParticipantStatusChanged{
			Alias: alias,
			From:  from,
			To:    p.Status,
			Since: p.Since,
		})
		candidates = append(candidates, keepaliveCandidate{alias: alias, agent: keepaliver})
	}
	s.mu.Unlock()
	return candidates, events
}
