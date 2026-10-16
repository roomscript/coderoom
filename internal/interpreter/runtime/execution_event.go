package runtime

import "github.com/roomscript/coderoom/internal/session"

// ExecutionEvent is a terminal fact from one invocation's execution capability.
// It is separate from Session's participant events and canonical publication.
type ExecutionEvent interface{ executionEvent() }

// DeliveryCompleted forwards the actual Session outcome, including partial or
// failed delivery in Result.Err. It does not report participant work completion.
type DeliveryCompleted struct {
	// ExecutionID is the identity returned by Session.Execute, shared by any work
	// started by this command. Delivery completion does not mean work completion.
	ExecutionID session.ExecutionID
	Result      session.RoutingResult
}

// ExecutionFailed reports a host execution/protocol failure with a non-nil error.
// It contains no fabricated Session routing kind or recipient description.
type ExecutionFailed struct{ Err error }

func (DeliveryCompleted) executionEvent() {}
func (ExecutionFailed) executionEvent()   {}
