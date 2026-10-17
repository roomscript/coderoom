package runtime

import "github.com/roomscript/coderoom/internal/session"

// ExecutionEvent is a terminal fact from one invocation's execution capability.
// It is separate from Session's participant events and canonical publication.
type ExecutionEvent interface{ executionEvent() }

// ExecutionCompleted reports that Session.Execute returned, not that participant
// work finished. Invite completion means startup launched; cancel completion means
// interruption requested; delivery completion means delivery attempts finished.
type ExecutionCompleted struct {
	ExecutionID session.ExecutionID
	// Result contains actual routing when present; an empty Kind means no routing.
	Result session.RoutingResult
	// Err is the authoritative Session.Execute error. For routing outcomes,
	// Result.Err carries the same error; it is not an independent result.
	Err error
}

// ExecutionFailed reports a host/protocol failure with a non-nil error.
// It contains no fabricated Session routing kind or recipient description.
type ExecutionFailed struct{ Err error }

func (ExecutionCompleted) executionEvent() {}
func (ExecutionFailed) executionEvent()    {}
