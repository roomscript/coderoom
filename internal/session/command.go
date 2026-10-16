package session

import (
	"errors"
	"slices"

	"github.com/roomscript/coderoom/internal/participant"
)

// DeliveryError reports that a command partially succeeded before returning an
// error. Delivered aliases accepted the turn; Err describes the failed
// deliveries.
type DeliveryError struct {
	Delivered []string
	Err       error
}

func (e *DeliveryError) Error() string {
	if e == nil || e.Err == nil {
		return ""
	}
	return e.Err.Error()
}

func (e *DeliveryError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func newDeliveryError(delivered []string, err error) error {
	if err == nil {
		return nil
	}
	cp := append([]string(nil), delivered...)
	slices.Sort(cp)
	return &DeliveryError{Delivered: cp, Err: err}
}

// DeliveredAliases returns the aliases that accepted a turn before err was
// returned. It returns nil when err does not carry partial-delivery metadata.
func DeliveredAliases(err error) []string {
	var deliveryErr *DeliveryError
	if !errors.As(err, &deliveryErr) || deliveryErr == nil {
		return nil
	}
	return append([]string(nil), deliveryErr.Delivered...)
}

// ExecutionID identifies one call to Session.Execute, successful or failed.
// All work started by that execution shares its ID. Zero is not an execution.
type ExecutionID = participant.ExecutionID

// Command is a sealed interface; only types in this package can implement it.
// Execute dispatches via the unexported method — no type switch required.
type Command interface {
	execute(s *Session, executionID ExecutionID) error
}
