package std

import (
	"github.com/roomscript/coderoom/internal/interpreter/runtime"
	"github.com/roomscript/coderoom/internal/room"
	"github.com/roomscript/coderoom/internal/session"
)

// deriveDeliveryCompletion preserves actual partial routing and original input.
// No submitted input record is created when delivery reaches nobody.
func deriveDeliveryCompletion(raw string, result session.RoutingResult, err error) runtime.CommandResult {
	if err != nil {
		return runtime.CommandResult{Err: err}
	}
	completion := runtime.CommandResult{Err: result.Err}
	delivered := result.Aliases(session.DeliveryDelivered)
	if len(delivered) == 0 {
		return completion
	}
	completion.Records = []room.Record{{
		Kind: room.KindUserInput, Text: raw, Routing: delivered,
		FailedRouting: result.Aliases(session.DeliveryFailed),
		UnsentRouting: result.Aliases(session.DeliveryNotAttempted),
	}}
	return completion
}
