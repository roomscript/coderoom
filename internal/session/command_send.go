package session

import (
	"errors"
	"fmt"
	"slices"

	"github.com/roomscript/coderoom/internal/agent"
	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/policy"
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
		for _, value := range s.Participants() {
			if !value.IsRoutable() {
				continue
			}
			aliases = append(aliases, value.Alias)
		}
	}
	s.notify(Broadcast{Text: c.Text})
	var errs []error
	var delivered []string
	for _, alias := range aliases {
		p, ok := s.readParticipantRuntime(alias)
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

// SendToParticipantCommand sends a message to one agent in the shared room.
// Message is sent to the primary participant. When send-notices is enabled,
// Notice is sent to the planned notice recipients. The caller is responsible for
// both texts — the session controller does not construct or format messages.
// One SharedSend event is emitted to observers.
type SendToParticipantCommand struct {
	Plan    ParticipantSendPlan
	Message string
	Notice  string
}

// ParticipantSendPlan is an immutable, session-bound routing decision. Planning
// freezes recipients but does not reserve their availability.
type ParticipantSendPlan struct {
	session       *Session
	primaryAlias  string
	noticeAliases []string
}

// CreateParticipantSendPlan freezes the policy-aware targets for an in-room participant send.
func (s *Session) CreateParticipantSendPlan(primaryAlias string) ParticipantSendPlan {
	plan := ParticipantSendPlan{session: s, primaryAlias: primaryAlias}
	if !s.policies.Enabled(policy.SendNotices) {
		return plan
	}
	for _, p := range s.Participants() {
		if p.Status == participant.StatusCrashed {
			continue
		}
		if p.Alias != primaryAlias {
			plan.noticeAliases = append(plan.noticeAliases, p.Alias)
		}
	}
	slices.Sort(plan.noticeAliases)
	return plan
}

// Targets returns a copy of the aliases frozen into the plan, with the
// primary participant first.
func (p ParticipantSendPlan) Targets() []string {
	if p.session == nil || p.primaryAlias == "" {
		return nil
	}
	targets := make([]string, 1, len(p.noticeAliases)+1)
	targets[0] = p.primaryAlias
	return append(targets, p.noticeAliases...)
}

// DiscardUnavailableNoticeRecipients returns a copied plan without the named
// unavailable notice recipients. It cannot add recipients or remove the primary
// participant.
func (p ParticipantSendPlan) DiscardUnavailableNoticeRecipients(aliases []string) ParticipantSendPlan {
	if len(aliases) == 0 || len(p.noticeAliases) == 0 {
		return p
	}
	filtered := p
	filtered.noticeAliases = slices.DeleteFunc(
		slices.Clone(p.noticeAliases),
		func(alias string) bool { return slices.Contains(aliases, alias) },
	)
	return filtered
}

func (p ParticipantSendPlan) validate(s *Session) error {
	if p.session == nil || p.primaryAlias == "" {
		return fmt.Errorf("shared send plan is invalid")
	}
	if p.session != s {
		return fmt.Errorf("shared send plan belongs to another session")
	}
	return nil
}

func (c SendToParticipantCommand) execute(s *Session) error {
	if err := c.Plan.validate(s); err != nil {
		return err
	}
	alias := c.Plan.primaryAlias
	a, err := acquireParticipantForDirectSend(alias, s)
	if err != nil {
		return err
	}
	if err := sendPreparedDirect(alias, a, c.Message, s); err != nil {
		return err
	}
	s.notify(SharedSend{Alias: alias, Text: c.Message})
	delivered, err := sendSharedNotices(c.Plan.noticeAliases, c.Notice, s)
	if err != nil {
		return newDeliveryError(append([]string{alias}, delivered...), err)
	}
	return nil
}

// SendToParticipantOutsideRoomCommand sends an outgoing message without a room
// record or notices to other participants. Agent responses still use ordinary
// session events and can appear in the room.
type SendToParticipantOutsideRoomCommand struct {
	Alias string
	Text  string
}

func (c SendToParticipantOutsideRoomCommand) execute(s *Session) error {
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
	if p.HasActiveTurn() {
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

func sendPreparedDirect(alias string, a agent.Agent, text string, s *Session) error {
	anchorID, err := a.Send(text)
	if err != nil {
		s.abortWork(alias)
		return fmt.Errorf("send to %q: %w", alias, err)
	}
	s.beginParticipantWorking(alias, anchorID)
	return nil
}

func sendSharedNotices(noticeAliases []string, text string, s *Session) ([]string, error) {
	var errs []error
	var delivered []string
	for _, alias := range noticeAliases {
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
