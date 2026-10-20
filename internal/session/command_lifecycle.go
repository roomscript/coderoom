package session

import (
	"errors"
	"fmt"

	"github.com/roomscript/coderoom/internal/agent"
	"github.com/roomscript/coderoom/internal/agent/echo"
	roomconfig "github.com/roomscript/coderoom/internal/config"
	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/policy"
)

// InviteCommand adds an agent to the session and starts it.
// The session uses its AgentFactory to construct the agent for the given alias.
type InviteCommand struct {
	Alias string
}

func (c InviteCommand) execute(s *Session, _ ExecutionID) error {
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
	s.CreateAgentRuntime(p.Alias)
	a, err := s.createAgent(p.Alias, cfg)
	if err != nil {
		s.abandonInvite(p.Alias)
		return fmt.Errorf("invite %q: %w", p.Alias.Name, err)
	}
	s.hasInvited = true
	s.notify(ParticipantStatusChanged{Alias: p.Alias, From: "", To: p.Status, Since: p.Since, ActiveExecutionID: p.ActiveExecutionID})
	s.notifyChangedConfig(p.Alias)
	s.notify(AgentStarting{Alias: p.Alias, Backend: a.Name()})
	startInvitedAgent(p.Alias, a, s)
	return nil
}

// buildAgent builds echo when the EchoInvites policy is on, and the
// configured backend otherwise. Starting a backend's agent tests more than
// its dependency check, so a check still running for it is cancelled.
func (s *Session) buildAgent(cfg roomconfig.ParticipantConfig) (agent.Agent, error) {
	if s.policies.Enabled(policy.EchoInvites) {
		return echo.New(), nil
	}
	s.cancelDependencyCheck(cfg.Backend.Name)
	return s.agentFactory(s, cfg)
}

// createAgent builds the participant's agent and records the backend name it
// reports in the participant's settings.
func (s *Session) createAgent(alias participant.Alias, cfg roomconfig.ParticipantConfig) (agent.Agent, error) {
	a, err := s.buildAgent(cfg)
	if err != nil {
		return nil, err
	}
	err = s.updateParticipant(alias, func(p *participant.Participant) (Event, error) {
		p.Settings = agent.Settings{Backend: a.Name()}
		return nil, nil
	})
	if err != nil {
		return nil, err
	}
	return a, nil
}

// abandonInvite removes a participant whose agent could not be created.
// Nothing about it has been announced yet.
func (s *Session) abandonInvite(alias participant.Alias) {
	s.cancelAgentContext(alias)
	s.changeParticipants(func() { s.removeParticipantLocked(alias) })
}

func (s *Session) resolveParticipantConfig(alias string) (roomconfig.ParticipantConfig, error) {
	if s.config == nil {
		return roomconfig.ParticipantConfig{Alias: alias}, nil
	}
	cfg, err := s.config.Participant(alias)
	if err != nil {
		return roomconfig.ParticipantConfig{}, fmt.Errorf("resolve participant config for %q: %w", alias, err)
	}
	return cfg, nil
}

// notifyChangedConfig reports the participant's config files that changed
// since startup. The invite goes on with the loaded config.
func (s *Session) notifyChangedConfig(alias participant.Alias) {
	if s.config == nil {
		return
	}
	if sources := s.config.ChangedSources(alias.Name); len(sources) > 0 {
		s.notify(ParticipantConfigChanged{Alias: alias, Sources: sources})
	}
}

func (c InviteCommand) buildParticipant(s *Session, cfg roomconfig.ParticipantConfig) *participant.Participant {
	p := participant.New(cfg.Alias, cfg.Role, participant.InitiativeManual)
	p.BeginStartup(s.now())
	return p
}

func startInvitedAgent(alias participant.Alias, a agent.Agent, s *Session) {
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
			stopUnreadyParticipant(alias, a, s)
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
			stopUnreadyParticipant(alias, a, s)
			return
		}
		// Start the reader before dispatching AgentReady so the agent's pipe
		// is drained immediately. The participant is already StatusIdle with
		// StartupReady=true, so all invariants are satisfied.
		go s.readLoop(stop, alias, a)
		s.notifyAgentReady(alias)
	}()
}

// notifyAgentReady reports readiness with the participant's settings. A
// participant removed in the meantime still gets its event, without settings.
func (s *Session) notifyAgentReady(alias participant.Alias) {
	view, _ := s.Participant(alias)
	s.notify(AgentReady{Alias: alias, Settings: view.Settings})
}

func stopUnreadyParticipant(alias participant.Alias, a agent.Agent, s *Session) {
	s.cancelAgentContext(alias)
	s.changeParticipants(func() { s.removeParticipantLocked(alias) })
	_ = a.Stop()
}

func handleInviteStartError(alias participant.Alias, err error, s *Session) {
	s.cancelAgentContext(alias)
	s.notify(AgentLog{Alias: alias, Text: fmt.Sprintf("start failed: %v", err)})
	stateErr := s.updateParticipant(alias, func(p *participant.Participant) (Event, error) {
		from := p.Status
		p.Crash(s.now())
		return ParticipantStatusChanged{Alias: alias, From: from, To: p.Status, Since: p.Since, ActiveExecutionID: p.ActiveExecutionID}, nil
	})
	if stateErr != nil && !errors.Is(stateErr, errParticipantNotFound) {
		s.notifyParticipantInvariant(alias, stateErr)
	}
	s.notify(AgentCrashed{Alias: alias})
}

// RemoveCommand stops and removes an agent from the session.
type RemoveCommand struct {
	Alias participant.Alias
}

func (c RemoveCommand) execute(s *Session, _ ExecutionID) error {
	p, ok := s.detachParticipant(c.Alias)
	switch {
	case ok:
		// Normal path: reader detached, stop the agent process.
		if err := p.Agent.Stop(); err != nil {
			return fmt.Errorf("remove agent %q: %w", c.Alias.Name, err)
		}
		return nil
	case p != nil:
		// Participant exists but IsRemovable is false: still in the startup window.
		return fmt.Errorf("participant %q is not ready", c.Alias.Name)
	default:
		// No attached runtime: crashed before startup completed, or unknown alias.
		return s.evictCrashedBeforeStart(c.Alias)
	}
}

// CancelCommand matches /cancel alias: it requests interruption of current work.
// Interruption is asynchronous; the participant remains in the session.
type CancelCommand struct {
	Alias participant.Alias
}

func (c CancelCommand) execute(s *Session, _ ExecutionID) error {
	p, ok := s.lookupParticipant(c.Alias)
	if !ok {
		return fmt.Errorf("participant %q not found", c.Alias.Name)
	}
	if !p.IsCancellable() {
		return fmt.Errorf("participant %q not ready", c.Alias.Name)
	}
	if err := p.Agent.Interrupt(); err != nil {
		return fmt.Errorf("interrupt %q: %w", c.Alias.Name, err)
	}
	return nil
}
