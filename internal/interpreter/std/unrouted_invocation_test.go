package std_test

import (
	"errors"
	"testing"

	"github.com/roomscript/coderoom/internal/interpreter/runtime"
	"github.com/roomscript/coderoom/internal/interpreter/std"
	"github.com/roomscript/coderoom/internal/session"
)

type unroutedTestExecution struct {
	loopScope
	err error
}

func (s *unroutedTestExecution) Execute(command session.Command) error {
	s.command = command
	return s.err
}

type unroutedTestHost struct {
	*sendDelivery
	execution runtime.SessionExecution
}

func (h *unroutedTestHost) CreateExecution() runtime.SessionExecution { return h.execution }

func TestUnroutedInvocation_releasesScopeOnFailedAdmissionOrEarlyClose(t *testing.T) {
	for _, rejected := range []bool{false, true} {
		t.Run(map[bool]string{false: "close while running", true: "failed admission"}[rejected], func(t *testing.T) {
			execution := &unroutedTestExecution{}
			if rejected {
				execution.err = errors.New("admission rejected")
			}
			ctx := runtime.Context{Session: &unroutedTestHost{execution: execution}}
			invocation, err := (std.InviteCommand{}).Prepare(parseSendStatement(t, "/invite ada"), ctx)
			if err != nil {
				t.Fatal(err)
			}
			lifecycle := runtime.NewInvocationLifecycle(invocation, ctx)
			defer lifecycle.Close(errors.New("cleanup"))
			completions := 0
			waiting, err := lifecycle.Start(func(runtime.CommandResult) { completions++ })
			if waiting || !errors.Is(err, execution.err) || completions != 0 {
				t.Fatalf("waiting=%v error=%v completions=%d", waiting, err, completions)
			}
			if rejected && !execution.closed {
				t.Fatal("failed admission retained terminal subscription")
			}
			lifecycle.Close(errors.New("shutdown"))
			if !execution.closed {
				t.Fatal("closing unfinished invocation retained terminal subscription")
			}
		})
	}
}
