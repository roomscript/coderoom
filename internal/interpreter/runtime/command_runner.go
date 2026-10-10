package runtime

import (
	"errors"
	"fmt"

	"github.com/roomscript/coderoom/internal/room"
)

// ErrPendingInvocation means the synchronous proof cannot advance an invocation
// that returned neither records nor completion. Waiting support is not designed yet.
var ErrPendingInvocation = errors.New("pending invocations are not supported")

// CommandRunner drives commands that produce records synchronously. It owns no
// session, goroutine or publication path; callers supply serialized execution.
type CommandRunner struct{}

// Run preserves record order, including records in the final step. If an
// invocation becomes pending, it returns accumulated records and an error instead
// of polling. This API does not yet retain or resume pending work.
func (CommandRunner) Run(command Command, ctx Context) ([]room.Record, error) {
	invocation, err := command.Prepare(ctx)
	if err != nil {
		return nil, fmt.Errorf("prepare command: %w", err)
	}
	invocation.Init()
	var records []room.Record
	for {
		step := invocation.Next()
		records = append(records, step.Records...)
		if step.Done {
			return records, nil
		}
		if len(step.Records) == 0 {
			return records, ErrPendingInvocation
		}
	}
}
