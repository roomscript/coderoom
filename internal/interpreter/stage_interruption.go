package interpreter

import (
	"github.com/roomscript/coderoom/internal/participant"
)

// stageInterruption owns cancellation correlation and successful acknowledgements.
// Readiness remains owned by the stage; cancellation success does not imply idle.
type stageInterruption struct {
	requested bool
	pending   map[workflowRef]participant.Alias
	cancelled []participant.Alias
}

// completeCancellation consumes a reply and remembers successful cancellations.
// Failed aliases remain eligible for retry; duplicates and stale replies do nothing.
func (s *stageInterruption) completeCancellation(outcome sessionOutcome) bool {
	alias, ok := s.pending[outcome.target]
	if !ok {
		return false
	}
	delete(s.pending, outcome.target)
	if outcome.err == nil {
		s.cancelled = append(s.cancelled, alias)
		sortParticipantAliases(s.cancelled)
	}
	return true
}
