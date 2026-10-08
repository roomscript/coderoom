package session

import "slices"

// RoutingKind identifies the operation whose delivery attempts have finished.
type RoutingKind string

// Routing operation kinds.
const (
	RoutingParticipantSend RoutingKind = "participant-send"
	RoutingOutsideRoomSend RoutingKind = "outside-room-send"
	RoutingBroadcast       RoutingKind = "broadcast"
	RoutingHandoff         RoutingKind = "handoff"
)

// RecipientRole distinguishes work recipients from context-only recipients.
type RecipientRole string

// Roles within a routing operation.
const (
	RecipientPrimary   RecipientRole = "primary"
	RecipientNotice    RecipientRole = "notice"
	RecipientBroadcast RecipientRole = "broadcast"
	RecipientHandoff   RecipientRole = "handoff"
)

// DeliveryStatus distinguishes adapter acceptance, rejection, and skipped delivery.
type DeliveryStatus string

// Delivery outcomes for each intended recipient.
const (
	DeliveryDelivered    DeliveryStatus = "delivered"
	DeliveryFailed       DeliveryStatus = "failed"
	DeliveryNotAttempted DeliveryStatus = "not-attempted"
)

// RecipientResult describes one intended delivery, in plan order. Delivered means
// adapter acceptance, not completed work. Failed includes readiness rejection;
// NotAttempted means delivery was skipped (for example, the primary send failed).
// Err is populated for failed recipients; command-wide rejection is in Result.Err.
type RecipientResult struct {
	Alias  string
	Role   RecipientRole
	Status DeliveryStatus
	Err    error
}

// RoutingResult reports actual outcomes for the command's execution-time plan.
// Recipients includes every intended delivery, even failures and skipped notices.
// Err is the command error and may be non-nil even when some deliveries succeeded.
type RoutingResult struct {
	Kind       RoutingKind
	Recipients []RecipientResult
	Err        error
}

// Clone detaches recipient storage for observers and retained projections.
func (r RoutingResult) Clone() RoutingResult {
	r.Recipients = slices.Clone(r.Recipients)
	return r
}

// Aliases returns recipients with the given actual outcome, in plan order.
func (r RoutingResult) Aliases(status DeliveryStatus) []string {
	var aliases []string
	for _, recipient := range r.Recipients {
		if recipient.Status == status {
			aliases = append(aliases, recipient.Alias)
		}
	}
	return aliases
}

func createRoutingResult(kind RoutingKind, aliases []string, role RecipientRole) RoutingResult {
	result := RoutingResult{Kind: kind}
	for _, alias := range aliases {
		result.Recipients = append(result.Recipients, RecipientResult{
			Alias: alias, Role: role, Status: DeliveryNotAttempted,
		})
	}
	return result
}

func (r *RoutingResult) record(index int, err error) {
	recipient := &r.Recipients[index]
	recipient.Err = err
	recipient.Status = DeliveryDelivered
	if err != nil {
		recipient.Status = DeliveryFailed
	}
}

func (s *Session) notifyRoutingCompleted(result RoutingResult, err error) {
	result.Err = err
	s.notify(RoutingCompleted{Result: result.Clone()})
}
