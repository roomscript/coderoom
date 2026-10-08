package session

import (
	"errors"
	"fmt"

	"github.com/roomscript/coderoom/internal/agent"
	roomconfig "github.com/roomscript/coderoom/internal/config"
	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/policy"
)

// InviteCommand adds an agent to the session and starts it.
// The session uses its AgentFactory to construct the agent for the given alias.
type InviteCommand struct {
	Alias string
}

func (c InviteCommand) execute(s *Session) error {
	backend := AgentBackendDefault
	if s.policies.Enabled(policy.EchoInvites) {
		backend = AgentBackendEcho
	}
	if s.agentFactory == nil {
		return fmt.Errorf("no agent factory configured on session")
	}
	cfg, err := s.resolveParticipantConfig(c.Alias)
	if err != nil {
		return err
	}
	p := c.buildParticipant(s, cfg)
	if err := s.addParticipant(p); err != nil {
		return err
	}
	s.hasInvited = true
	s.CreateAgentRuntime(c.Alias)
	a := s.agentFactory(s, cfg, backend)
	s.notify(ParticipantStatusChanged{Alias: c.Alias, From: "", To: p.Status, Since: p.Since})
	s.notify(AgentStarting(c))
	startInvitedAgent(c.Alias, a, s)
	return nil
}

func (s *Session) resolveParticipantConfig(alias string) (roomconfig.ParticipantConfig, error) {
	if s.config == nil {
		return roomconfig.ParticipantConfig{Alias: alias}, nil
	}
	cfg, err := s.config.ForParticipant(alias)
	if err != nil {
		return roomconfig.ParticipantConfig{}, fmt.Errorf("resolve participant config for %q: %w", alias, err)
	}
	return cfg, nil
}

func (c InviteCommand) buildParticipant(s *Session, cfg roomconfig.ParticipantConfig) *participant.Participant {
	p := participant.New(cfg.Alias, cfg.Role, participant.InitiativeManual)
	p.BeginStartup(s.now())
	return p
}

func startInvitedAgent(alias string, a agent.Agent, s *Session) {
	go func() {
		if err := a.Start(); err != nil {
			handleInviteStartError(alias, err, s)
			return
		}
		stop, from, ok := s.attachParticipant(alias, a)
		if !ok {
			s.cancelAgentContext(alias)
			_ = a.Stop()
			return
		}
		// Transition to Idle first so IsSendable is true when AgentReady fires.
		// The status-change event uses the pre-attach status as From so observers
		// see Starting → Idle rather than the internal Attached intermediate state.
		ev, ok := s.commitStarted(alias, from)
		if !ok {
			// Invariant violation: clean up the stranded agents entry.
			s.mu.Lock()
			delete(s.agents, alias)
			_ = s.registry.Remove(alias)
			s.mu.Unlock()
			s.cancelAgentContext(alias)
			_ = a.Stop()
			return
		}
		s.notify(ev)
		// Mark the participant session-ready before dispatching AgentReady.
		// IsRemovable gates on StartupReady, so /remove cannot succeed until
		// after the event fires. Go memory model: the channel send inside
		// notify happens-after this write, so any goroutine that receives
		// AgentReady is guaranteed to observe StartupReady=true when it
		// subsequently calls detachParticipant.
		if err := s.updateParticipant(alias, func(p *participant.Participant) (Event, error) {
			return nil, p.SessionReady()
		}); err != nil {
			s.notifyParticipantInvariant(alias, fmt.Errorf("session ready: %w", err))
			s.mu.Lock()
			delete(s.agents, alias)
			_ = s.registry.Remove(alias)
			s.mu.Unlock()
			s.cancelAgentContext(alias)
			_ = a.Stop()
			return
		}
		// Start the reader before dispatching AgentReady so the agent's pipe
		// is drained immediately. The participant is already StatusIdle with
		// StartupReady=true, so all invariants are satisfied.
		go s.readLoop(stop, alias, a)
		s.notify(AgentReady{Alias: alias})
	}()
}

func handleInviteStartError(alias string, err error, s *Session) {
	s.cancelAgentContext(alias)
	s.notify(AgentLog{Alias: alias, Text: fmt.Sprintf("start failed: %v", err)})
	stateErr := s.updateParticipant(alias, func(p *participant.Participant) (Event, error) {
		from := p.Status
		p.Crash(s.now())
		return ParticipantStatusChanged{Alias: alias, From: from, To: p.Status, Since: p.Since}, nil
	})
	if stateErr != nil && !errors.Is(stateErr, errParticipantNotFound) {
		s.notifyParticipantInvariant(alias, stateErr)
	}
	s.notify(AgentCrashed{Alias: alias})
}

// RemoveCommand stops and removes an agent from the session.
type RemoveCommand struct {
	Alias string
}

func (c RemoveCommand) execute(s *Session) error {
	p, ok := s.detachParticipant(c.Alias)
	switch {
	case ok && p != nil:
		// Normal path: reader detached, stop the agent process.
		if err := p.Agent.Stop(); err != nil {
			return fmt.Errorf("remove agent %q: %w", c.Alias, err)
		}
		return nil
	case ok && p == nil:
		// Attached runtime existed but the registry was inconsistent; best-effort cleanup.
		s.notify(AgentStopped(c))
		return nil
	case !ok && p != nil:
		// Participant exists but IsRemovable is false: still in the startup window.
		return fmt.Errorf("participant %q is not ready", c.Alias)
	default:
		// No attached runtime: crashed before startup completed, or unknown alias.
		return s.evictCrashedBeforeStart(c.Alias)
	}
}

// CancelCommand matches /cancel alias: it requests interruption of current work.
// Interruption is asynchronous; the participant remains in the session.
type CancelCommand struct {
	Alias string
}

func (c CancelCommand) execute(s *Session) error {
	p, ok := s.lookupParticipant(c.Alias)
	if !ok {
		return fmt.Errorf("participant %q not found", c.Alias)
	}
	if !p.IsCancellable() {
		return fmt.Errorf("participant %q not ready", c.Alias)
	}
	if err := p.Agent.Interrupt(); err != nil {
		return fmt.Errorf("interrupt %q: %w", c.Alias, err)
	}
	return nil
}
