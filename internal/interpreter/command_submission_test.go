package interpreter

import (
	"errors"
	"testing"

	"github.com/roomscript/coderoom/internal/interpreter/runtime"
	"github.com/roomscript/coderoom/internal/promptlang"
	"github.com/roomscript/coderoom/internal/session"
)

type milestoneCommand struct {
	invocations []*milestoneInvocation
}

func (*milestoneCommand) Name() string                    { return "loop" }
func (*milestoneCommand) Usage() string                   { return "/loop" }
func (*milestoneCommand) Description() string             { return "submission boundary proof" }
func (*milestoneCommand) Statement() promptlang.Statement { return promptlang.Loop{} }
func (c *milestoneCommand) Prepare(_ promptlang.ParsedStatement, _ runtime.Context) (runtime.Invocation, error) {
	inv := &milestoneInvocation{}
	c.invocations = append(c.invocations, inv)
	return inv, nil
}

type milestoneInvocation struct {
	ready    bool
	complete func(runtime.CommandResult)
}

func (*milestoneInvocation) Staging() *runtime.Staging  { return nil }
func (*milestoneInvocation) Close()                     {}
func (i *milestoneInvocation) ReadyToAcknowledge() bool { return i.ready }
func (i *milestoneInvocation) Go(complete func(runtime.CommandResult)) error {
	i.complete = complete
	return nil
}

func TestCommandSubmission_milestoneAndLoopAdmission(t *testing.T) {
	interp, command, events := newMilestoneInterpreter(t)
	const first = "/loop @ada work /until /tests /max 1"
	const second = "/loop @ada other /until /undefined /max 1"
	if err := interp.Submit(first); err != nil {
		t.Fatal(err)
	}
	interp.Snapshot()
	assertNoSubmissionOutcome(t, events)
	if err := interp.Submit(second); err != nil {
		t.Fatal(err)
	}
	awaitLifecycleEvent(t, events, func(event Event) bool {
		failed, ok := event.(SubmissionFailed)
		return ok && failed.Raw == second
	})
	if len(command.invocations) != 1 {
		t.Fatal("second loop reached preparation")
	}
	if err := interp.fallback.Schedule(func() {
		command.invocations[0].ready = true
		for id := range interp.invocations {
			interp.processCommandEvents(id)
			interp.processCommandEvents(id)
		}
	}); err != nil {
		t.Fatal(err)
	}
	awaitLifecycleEvent(t, events, func(event Event) bool {
		succeeded, ok := event.(SubmissionSucceeded)
		return ok && succeeded.Raw == first
	})
	if err := interp.fallback.Schedule(func() { command.invocations[0].complete(runtime.CommandResult{}) }); err != nil {
		t.Fatal(err)
	}
	interp.Snapshot()
	assertNoSubmissionOutcome(t, events)
	assertLoopReadmitted(t, interp, command, second)
}

func assertLoopReadmitted(t *testing.T, interp *Interpreter, command *milestoneCommand, input string) {
	t.Helper()
	if err := interp.Submit(input); err != nil {
		t.Fatal(err)
	}
	interp.Snapshot()
	if len(command.invocations) != 2 {
		t.Fatal("settled loop still prevents admission")
	}
}

func TestCommandSubmission_completionBeforeMilestone(t *testing.T) {
	failure := errors.New("delivery failed")
	for _, outcome := range []struct {
		name string
		err  error
	}{{"success", nil}, {"failure", failure}} {
		t.Run(outcome.name, func(t *testing.T) {
			events := make(chan Event, 128)
			interp := New(t.Context(), session.New(), t.TempDir(), WithObserver(routingObserver{events}))
			t.Cleanup(interp.Close)
			command := &milestoneCommand{}
			interp.commands = runtime.NewRegistry()
			if err := interp.commands.Register(command); err != nil {
				t.Fatal(err)
			}
			if err := interp.Submit("/loop @ada work /until /tests /max 1"); err != nil {
				t.Fatal(err)
			}
			interp.Snapshot()
			if err := interp.fallback.Schedule(func() { command.invocations[0].complete(runtime.CommandResult{Err: outcome.err}) }); err != nil {
				t.Fatal(err)
			}
			awaitLifecycleEvent(t, events, func(event Event) bool {
				if outcome.err == nil {
					_, ok := event.(SubmissionSucceeded)
					return ok
				}
				failed, ok := event.(SubmissionFailed)
				return ok && errors.Is(failed.Err, outcome.err)
			})
			interp.Snapshot()
			assertNoSubmissionOutcome(t, events)
		})
	}
}

func assertNoSubmissionOutcome(t *testing.T, events <-chan Event) {
	t.Helper()
	for {
		select {
		case event := <-events:
			switch event.(type) {
			case SubmissionSucceeded, SubmissionFailed, OperationFailed:
				t.Fatalf("unexpected submission outcome: %#v", event)
			}
		default:
			return
		}
	}
}

func newMilestoneInterpreter(t *testing.T) (*Interpreter, *milestoneCommand, chan Event) {
	t.Helper()
	events := make(chan Event, 128)
	interp := New(t.Context(), session.New(), t.TempDir(), WithObserver(routingObserver{events}))
	t.Cleanup(interp.Close)
	command := &milestoneCommand{}
	interp.commands = runtime.NewRegistry()
	if err := interp.commands.Register(command); err != nil {
		t.Fatal(err)
	}
	return interp, command, events
}
