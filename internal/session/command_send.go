package session

import (
	"errors"
	"fmt"
	"slices"

	"github.com/trigosec/coderoom/internal/agent"
	"github.com/trigosec/coderoom/internal/participant"
	"github.com/trigosec/coderoom/internal/policy"
)

// BroadcastCommand sends a message to its frozen recipients. When Aliases is
// nil, execution selects all currently routable participants for legacy callers.
type BroadcastCommand struct {
	Aliases []string
	Text    string
}

func (c BroadcastCommand) execute(s *Session) error {
	aliases := c.Aliases
	if aliases == nil {
		for _, value := range s.RoutableParticipants() {
			aliases = append(aliases, value.Alias)
		}
	}
	s.notify(Broadcast{Text: c.Text})
	var errs []error
	var delivered []string
	for _, alias := range aliases {
		p, ok := s.Participant(alias)
		if !ok {
			errs = append(errs, fmt.Errorf("broadcast to %q: %w", alias, errParticipantNotFound))
			continue
		}
		err := s.prepareParticipantForWork(p.Alias)
		if err != nil {
			if !errors.Is(err, errParticipantNotFound) {
				s.notifyParticipantInvariant(p.Alias, err)
			}
			errs = append(errs, fmt.Errorf("broadcast to %q: %w", p.Alias, err))
			continue
		}
		anchorID, err := p.Agent.Send(c.Text)
		if err != nil {
			s.abortWork(p.Alias)
			errs = append(errs, fmt.Errorf("broadcast to %q: %w", p.Alias, err))
			continue
		}
		s.beginParticipantWorking(p.Alias, anchorID)
		delivered = append(delivered, p.Alias)
	}
	joined := errors.Join(errs...)
	if joined != nil {
		return newDeliveryError(delivered, joined)
	}
	return nil
}

// SharedSendCommand sends a message to one agent in the shared room.
// TextDirect is sent to the addressed agent. When send-notices is enabled,
// TextListeners is sent to all other agents. The caller is responsible for
// both texts — the session controller does not construct or format messages.
// One SharedSend event is emitted to observers.
type SharedSendCommand struct {
	Plan          SharedSendPlan
	TextDirect    string
	TextListeners string
}

// SharedSendPlan is an immutable, session-bound routing decision. Planning
// freezes recipients but does not reserve their availability.
type SharedSendPlan struct {
	session         *Session
	addressedAlias  string
	listenerAliases []string
}

// PlanSharedSend freezes the policy-aware targets for a direct shared send.
func (s *Session) PlanSharedSend(addressedAlias string) SharedSendPlan {
	plan := SharedSendPlan{session: s, addressedAlias: addressedAlias}
	if !s.policies.Enabled(policy.SendNotices) {
		return plan
	}
	for _, p := range s.RoutableParticipants() {
		if p.Alias != addressedAlias {
			plan.listenerAliases = append(plan.listenerAliases, p.Alias)
		}
	}
	slices.Sort(plan.listenerAliases)
	return plan
}

// Targets returns a copy of the aliases frozen into the plan, with the
// addressed participant first.
func (p SharedSendPlan) Targets() []string {
	if p.session == nil || p.addressedAlias == "" {
		return nil
	}
	targets := make([]string, 1, len(p.listenerAliases)+1)
	targets[0] = p.addressedAlias
	return append(targets, p.listenerAliases...)
}

// DiscardUnavailableListeners returns a copied plan without the named
// unavailable listeners. It cannot add recipients or remove the addressed
// participant.
func (p SharedSendPlan) DiscardUnavailableListeners(aliases []string) SharedSendPlan {
	if len(aliases) == 0 || len(p.listenerAliases) == 0 {
		return p
	}
	filtered := p
	filtered.listenerAliases = slices.DeleteFunc(
		slices.Clone(p.listenerAliases),
		func(alias string) bool { return slices.Contains(aliases, alias) },
	)
	return filtered
}

func (p SharedSendPlan) validate(s *Session) error {
	if p.session == nil || p.addressedAlias == "" {
		return fmt.Errorf("shared send plan is invalid")
	}
	if p.session != s {
		return fmt.Errorf("shared send plan belongs to another session")
	}
	return nil
}

func (c SharedSendCommand) execute(s *Session) error {
	if err := c.Plan.validate(s); err != nil {
		return err
	}
	alias := c.Plan.addressedAlias
	a, err := acquireParticipantForDirectSend(alias, s)
	if err != nil {
		return err
	}
	if err := sendPreparedDirect(alias, a, c.TextDirect, s); err != nil {
		return err
	}
	s.notify(SharedSend{Alias: alias, Text: c.TextDirect})
	delivered, err := sendSharedNotices(c.Plan.listenerAliases, c.TextListeners, s)
	if err != nil {
		return newDeliveryError(append([]string{alias}, delivered...), err)
	}
	return nil
}

// PrivateSendCommand sends a message directly to one agent's private channel.
// Nothing is emitted to the shared room and no other agents are notified.
// Used for approval flows and reasoning that should not pollute the shared room.
type PrivateSendCommand struct {
	Alias string
	Text  string
}

func (c PrivateSendCommand) execute(s *Session) error {
	a, err := acquireParticipantForDirectSend(c.Alias, s)
	if err != nil {
		return err
	}
	anchorID, sendErr := a.Send(c.Text)
	if sendErr != nil {
		s.abortWork(c.Alias)
		return fmt.Errorf("send to %q: %w", c.Alias, sendErr)
	}
	s.beginParticipantWorking(c.Alias, anchorID)
	return nil
}

// acquireParticipantForDirectSend captures the participant's agent and
// transitions it from Idle to Preparing. Direct sends require exclusivity:
// an already-working participant rejects the command.
func acquireParticipantForDirectSend(alias string, s *Session) (a agent.Agent, err error) {
	if err := s.prepareParticipantForWork(alias); err != nil {
		return nil, formatDirectSendPrepareError(alias, err, s)
	}
	p, ok := s.lookupParticipant(alias)
	if !ok || p.Agent == nil || !p.IsSendable() {
		return nil, fmt.Errorf("participant %q not ready", alias)
	}
	return p.Agent, nil
}

// acquireParticipantForNotice captures the participant's agent and transitions
// it to Preparing only when currently Idle. Existing Working/Preparing turns are
// allowed so a notice can be layered onto an active turn.
func acquireParticipantForNotice(alias string, s *Session) (a agent.Agent, prepared bool, err error) {
	p, err := lookupSendableParticipant(alias, s)
	if err != nil {
		return nil, false, err
	}
	if isActiveParticipantStatus(p.Status) {
		return p.Agent, false, nil
	}
	a, err = acquireParticipantForDirectSend(alias, s)
	if err != nil {
		return nil, false, err
	}
	return a, true, nil
}

func formatDirectSendPrepareError(alias string, err error, s *Session) error {
	if errors.Is(err, errParticipantNotFound) {
		return fmt.Errorf("participant %q not found", alias)
	}
	s.notifyParticipantInvariant(alias, err)
	return fmt.Errorf("participant %q invalid working transition: %w", alias, err)
}

func lookupSendableParticipant(alias string, s *Session) (participant.Participant, error) {
	p, ok := s.lookupParticipant(alias)
	if !ok {
		return participant.Participant{}, fmt.Errorf("participant %q not found", alias)
	}
	if !p.IsSendable() || p.Agent == nil {
		return participant.Participant{}, fmt.Errorf("participant %q not ready", alias)
	}
	return p.Snapshot(), nil
}

func isActiveParticipantStatus(status participant.Status) bool {
	return status == participant.StatusWorking || status == participant.StatusPreparing
}

func sendPreparedDirect(alias string, a agent.Agent, text string, s *Session) error {
	anchorID, err := a.Send(text)
	if err != nil {
		s.abortWork(alias)
		return fmt.Errorf("send to %q: %w", alias, err)
	}
	s.beginParticipantWorking(alias, anchorID)
	return nil
}

func sendSharedNotices(listenerAliases []string, text string, s *Session) ([]string, error) {
	var errs []error
	var delivered []string
	for _, alias := range listenerAliases {
		a, prepared, err := acquireParticipantForNotice(alias, s)
		if err != nil {
			errs = append(errs, fmt.Errorf("notice to %q: %w", alias, err))
			continue
		}
		anchorID, err := a.SendNotice(text)
		if err != nil {
			s.abortWork(alias)
			errs = append(errs, fmt.Errorf("notice to %q: %w", alias, err))
			continue
		}
		if prepared {
			s.beginParticipantWorking(alias, anchorID)
		} else {
			s.trackAnchorStream(alias, anchorID)
		}
		s.notify(SharedNotice{Alias: alias, Text: text})
		delivered = append(delivered, alias)
	}
	return delivered, errors.Join(errs...)
}
