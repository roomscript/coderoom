package runtime

import (
	"errors"
	"testing"
)

type eventProcessingInvocation struct {
	*waitingInvocation
	processed int
}

func (i *eventProcessingInvocation) HandlePendingEvents() { i.processed++ }

func TestInvocationLifecycle_processesEventsOnlyWhileRunning(t *testing.T) {
	for _, state := range []string{"prepared", "waiting", "running", "failed start", "completed", "closed"} {
		t.Run(state, func(t *testing.T) {
			_, host, waiting := newWaitingLifecycle()
			if state != "waiting" {
				waiting.staging = nil
			}
			if state == "failed start" {
				waiting.launchErr = errors.New("launch failed")
			}
			invocation := &eventProcessingInvocation{waitingInvocation: waiting}
			lifecycle := NewInvocationLifecycle(invocation, Context{Session: host})
			defer lifecycle.Close(errors.New("cleanup"))
			if state != "prepared" {
				_, err := lifecycle.Start(func(CommandResult) {})
				if !errors.Is(err, waiting.launchErr) {
					t.Fatalf("Start=%v", err)
				}
			}
			settleProcessingTestLifecycle(lifecycle, state)
			lifecycle.ProcessEvents()
			want := 0
			if state == "running" {
				want = 1
			}
			if invocation.processed != want {
				t.Fatalf("processed=%d, want %d", invocation.processed, want)
			}
		})
	}
}

func settleProcessingTestLifecycle(lifecycle *InvocationLifecycle, state string) {
	switch state {
	case "completed":
		lifecycle.Complete(CommandResult{})
	case "closed":
		lifecycle.Close(errors.New("closed"))
	}
}
