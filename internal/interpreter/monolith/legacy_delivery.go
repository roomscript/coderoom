package monolith

import (
	"errors"
	"fmt"

	"github.com/roomscript/coderoom/internal/interpreter/runtime"
	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/session"
)

// legacyCommandSession preserves SubmitCommand's early rejection of overlapping
// queued sends. This is compatibility admission, not participant availability or
// result correlation. Native facade commands use independent scopes without these reservations. Remove
// this wrapper and its reservations with the SubmitCommand bridge (checkpoint 9).
type legacyCommandSession struct{ *commandSession }

var _ runtime.SessionExecution = (*legacyCommandSession)(nil)

func (s *legacyCommandSession) Execute(command session.Command) error {
	if err := s.reserve(command); err != nil {
		return err
	}
	if err := s.commandSession.Execute(command); err != nil {
		s.release()
		return fmt.Errorf("legacy delivery: %w", err)
	}
	return nil
}

func (s *legacyCommandSession) reserve(command session.Command) error {
	send, ok := command.(session.SendToParticipantCommand)
	if !ok {
		return nil
	}
	e := s.provider.executor
	e.enqueueMu.Lock()
	defer e.enqueueMu.Unlock()
	primary := send.Plan.PrimaryParticipant()
	if e.legacySends[primary] != nil {
		return errors.New("legacy primary already has an admitted send")
	}
	if e.legacySends == nil {
		e.legacySends = make(map[participant.Alias]*legacyCommandSession)
	}
	e.legacySends[primary] = s
	return nil
}

func (s *legacyCommandSession) release() {
	e := s.provider.executor
	e.enqueueMu.Lock()
	defer e.enqueueMu.Unlock()
	for alias, owner := range e.legacySends {
		if owner == s {
			delete(e.legacySends, alias)
		}
	}
}

// The compatibility provider wraps each fresh scope independently. Its admission
// reservation remains private to SubmitCommand, never native participant policy.
type legacyCommandSessionProvider struct{ *commandSessionProvider }

func (s *legacyCommandSessionProvider) CreateExecution() runtime.SessionExecution {
	execution := s.createExecution()
	legacy := &legacyCommandSession{commandSession: execution}
	execution.mu.Lock()
	execution.onClose = legacy.release
	execution.mu.Unlock()
	return legacy
}
