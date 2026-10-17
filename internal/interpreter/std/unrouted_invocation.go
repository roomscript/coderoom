package std

import (
	"errors"
	"fmt"

	"github.com/roomscript/coderoom/internal/interpreter/runtime"
	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/session"
)

// unroutedInvocation executes Session commands without routing-dependent behavior.
// The interpreter owns acknowledgement, completion acceptance and causal publication.
type unroutedInvocation struct {
	session     runtime.SessionExecutionProvider
	command     session.Command
	unsubscribe func()
}

func (*unroutedInvocation) Staging() *runtime.Staging { return nil }

// ReadyToAcknowledge keeps submission pending until Session execution completes.
func (*unroutedInvocation) ReadyToAcknowledge() bool { return false }

func (i *unroutedInvocation) Go(complete func(runtime.CommandResult)) error {
	execution := i.session.CreateExecution()
	i.unsubscribe = execution.Subscribe(func(event runtime.ExecutionEvent) {
		complete(runtime.CommandResult{Err: unroutedExecutionError(event)})
	})
	if err := execution.Execute(i.command); err != nil {
		return fmt.Errorf("execute Session command: %w", err)
	}
	return nil
}

func unroutedExecutionError(event runtime.ExecutionEvent) error {
	switch event := event.(type) {
	case runtime.ExecutionCompleted:
		return event.Err
	case runtime.ExecutionFailed:
		if event.Err != nil {
			return event.Err
		}
	}
	return errors.New("invalid Session execution outcome")
}

func (i *unroutedInvocation) Close() {
	if i.unsubscribe != nil {
		i.unsubscribe()
		i.unsubscribe = nil
	}
}

// Preserve missing-target rejection in Session, with the requested name attached.
func resolveCommandParticipant(provider runtime.SessionExecutionProvider, name string) participant.Alias {
	if view, ok := provider.ParticipantByName(name); ok {
		return view.Alias
	}
	return participant.Alias{Name: name}
}
