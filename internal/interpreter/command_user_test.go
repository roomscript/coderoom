package interpreter

import (
	"context"
	"errors"
	"testing"

	"github.com/roomscript/coderoom/internal/agent"
	"github.com/roomscript/coderoom/internal/agent/echo"
	roomconfig "github.com/roomscript/coderoom/internal/config"
	"github.com/roomscript/coderoom/internal/interpreter/std"
	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/promptlang"
	"github.com/roomscript/coderoom/internal/session"
	"github.com/roomscript/coderoom/internal/shell"
)

func defineFacadeCommand(t *testing.T, interp *Interpreter, events <-chan Event, raw string) {
	t.Helper()
	parsed, err := promptlang.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	command, ok := interp.commands.Lookup(parsed)
	if _, definition := command.(std.DefineCommand); !ok || !definition {
		t.Fatal("definition is not selected by the facade registry")
	}
	if err := interp.Submit(raw); err != nil {
		t.Fatal(err)
	}
	awaitLifecycleEvent(t, events, func(event Event) bool { _, ok := event.(SubmissionSucceeded); return ok })
}

func TestUserCommand_nativeExecution(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "failure"}[failed], func(t *testing.T) {
			events := make(chan Event, 128)
			calls := make(chan string, 2)
			result := shell.Result{Status: shell.StatusSuccess, Stdout: "pass"}
			if failed {
				result = shell.Result{Status: shell.StatusFailure, Err: errors.New("shell failed")}
			}
			interp := New(t.Context(), session.New(), "/workspace", WithObserver(routingObserver{events}), WithShellRunner(ShellRunnerFunc(func(_ context.Context, _, program string) shell.Result { calls <- program; return result })))
			t.Cleanup(interp.Close)
			defineFacadeCommand(t, interp, events, "/def tests /shell run-tests")
			parsed, err := promptlang.Parse("/tests")
			if err != nil {
				t.Fatal(err)
			}
			command, selected := interp.commands.Lookup(parsed)
			if _, ok := command.(std.UserCommand); !selected || !ok {
				t.Fatal("named invocation is not modular")
			}
			if err := interp.Submit(parsed.Raw); err != nil {
				t.Fatal(err)
			}
			completed := awaitShellCompletion(t, events)
			interp.Close()
			assertNamedExecution(t, interp, completed, result, calls, parsed)
			assertNoShellOutcome(t, events)
		})
	}
}

func TestUserCommand_undefinedHasNoInputRecord(t *testing.T) {
	events := make(chan Event, 32)
	interp := New(t.Context(), session.New(), t.TempDir(), WithObserver(routingObserver{events}))
	t.Cleanup(interp.Close)
	if err := interp.Submit("  /missing  "); err != nil {
		t.Fatal(err)
	}
	awaitLifecycleEvent(t, events, func(event Event) bool {
		if _, ok := event.(InputAccepted); ok {
			t.Fatal("undefined command accepted")
		}
		unknown, ok := event.(UnknownCommand)
		if !ok {
			return false
		}
		var diagnostic *promptlang.Diagnostic
		if !errors.As(unknown.Err, &diagnostic) || diagnostic.Code != promptlang.DiagnosticUndefinedCommand || diagnostic.Span != (promptlang.Span{Start: 3, End: 10}) {
			t.Fatalf("diagnostic=%#v", unknown.Err)
		}
		return true
	})
	if len(interp.Snapshot().Room.Records) != 0 {
		t.Fatal("undefined command recorded input")
	}
}

func TestUserCommand_definitionFailures(t *testing.T) {
	for _, test := range []struct {
		name, raw string
		code      ErrorCode
	}{
		{"reserved", "/def help /shell true", ErrorReservedCommand},
		{"duplicate", "/def tests /shell changed", ErrorCommandExists},
	} {
		t.Run(test.name, func(t *testing.T) {
			events := make(chan Event, 64)
			interp := New(t.Context(), session.New(), t.TempDir(), WithObserver(routingObserver{events}))
			t.Cleanup(interp.Close)
			defineFacadeCommand(t, interp, events, "/def tests /shell original")
			if err := interp.Submit(test.raw); err != nil {
				t.Fatal(err)
			}
			awaitLifecycleEvent(t, events, func(event Event) bool {
				failure, ok := event.(SubmissionFailed)
				if !ok {
					return false
				}
				if failure.Code != test.code || failure.Statement.Raw != test.raw {
					t.Fatalf("failure=%#v", failure)
				}
				return true
			})
			parsed, err := promptlang.Parse("/tests")
			if err != nil {
				t.Fatal(err)
			}
			body, err := interp.ResolveCommand(parsed.Value.(promptlang.UserCommand))
			if err != nil || body.Program.Value != "original" {
				t.Fatalf("body=%#v, err=%v", body, err)
			}
		})
	}
}

func TestUserCommand_queuedInvocationFollowsDefinition(t *testing.T) {
	events := make(chan Event, 128)
	interp := New(t.Context(), session.New(), t.TempDir(), WithObserver(routingObserver{events}), WithShellRunner(ShellRunnerFunc(func(context.Context, string, string) shell.Result { return shell.Result{Status: shell.StatusSuccess} })))
	t.Cleanup(interp.Close)
	entered, release := make(chan struct{}), make(chan struct{})
	if err := interp.fallback.Schedule(func() { close(entered); <-release }); err != nil {
		t.Fatal(err)
	}
	<-entered
	definitionErr := interp.Submit("/def tests /shell true")
	invocationErr := interp.Submit("/tests")
	close(release)
	if definitionErr != nil || invocationErr != nil {
		t.Fatalf("submission errors=%v,%v", definitionErr, invocationErr)
	}
	awaitDefinitionThenInvocation(t, events)
	records := interp.Snapshot().Room.Records
	if len(records) != 4 || records[1].Text != "[defined] /tests" || records[2].Text != "/tests" {
		t.Fatalf("queued record order=%#v", records)
	}
}

func TestUserCommand_closeCancelsNamedWorker(t *testing.T) {
	events := make(chan Event, 128)
	started := make(chan struct{})
	interp := New(t.Context(), session.New(), t.TempDir(), WithObserver(routingObserver{events}), WithShellRunner(ShellRunnerFunc(func(ctx context.Context, _, _ string) shell.Result {
		close(started)
		<-ctx.Done()
		return shell.Result{Status: shell.StatusCancelled, Err: ctx.Err()}
	})))
	t.Cleanup(interp.Close)
	defineFacadeCommand(t, interp, events, "/def wait /shell wait")
	if err := interp.Submit("/wait"); err != nil {
		t.Fatal(err)
	}
	awaitShellSignal(t, started)
	interp.Close()
	completed := awaitShellCompletion(t, events)
	if completed.Command != "/wait" || completed.Result.Status != shell.StatusCancelled || !errors.Is(completed.Result.Err, context.Canceled) {
		t.Fatalf("completion=%#v", completed)
	}
	assertNoShellOutcome(t, events)
}

func TestUserCommand_definitionIsSharedWithLoop(t *testing.T) {
	ready := make(chan participant.Alias, 4)
	backend := &lifecycleAgent{Client: echo.New(), release: make(chan struct{})}
	sess := &lifecycleSession{Session: session.New(session.WithObserver(lifecycleReadyObserver{ready}), session.WithAgentFactory(func(*session.Session, roomconfig.ParticipantConfig) (agent.Agent, error) {
		return echo.New(), nil
	}))}
	events := make(chan Event, 128)
	calls := make(chan string, 2)
	interp := New(t.Context(), sess, t.TempDir(), WithObserver(routingObserver{events}), WithShellRunner(ShellRunnerFunc(func(_ context.Context, _, program string) shell.Result {
		calls <- program
		return shell.Result{Status: shell.StatusSuccess}
	})))
	t.Cleanup(interp.Close)
	t.Cleanup(backend.unblock)
	defineFacadeCommand(t, interp, events, "/def tests /shell run-tests")
	inviteStageRecipient(t, sess, ready, "ada")
	if err := interp.Submit("/loop @ada busy /until /tests /max 1"); err != nil {
		t.Fatal(err)
	}
	// Submission acknowledgement precedes condition output.
	awaitLifecycleEvent(t, events, func(event Event) bool {
		succeeded, ok := event.(SubmissionSucceeded)
		return ok && succeeded.Raw == "/loop @ada busy /until /tests /max 1"
	})
	backend.unblock()
	awaitLifecycleEvent(t, events, func(event Event) bool {
		if failed, ok := event.(OperationFailed); ok {
			t.Fatalf("loop failed: %v", failed.Err)
		}
		completed, ok := event.(ShellCompleted)
		return ok && completed.Command == "/tests"
	})
	interp.Close()
	if len(calls) != 1 || <-calls != "run-tests" {
		t.Fatal("loop did not resolve the shared definition")
	}
}

func assertNamedExecution(t *testing.T, interp *Interpreter, completed ShellCompleted, result shell.Result, calls <-chan string, parsed promptlang.ParsedStatement) {
	t.Helper()
	if completed.Command != "/tests" || completed.Statement.Raw != parsed.Raw || !errors.Is(completed.Result.Err, result.Err) {
		t.Fatalf("completion=%#v", completed)
	}
	if len(calls) != 1 || <-calls != "run-tests" {
		t.Fatal("definition was evaluated or invocation executed twice")
	}
	assertNamedRecords(t, interp)
}

func awaitDefinitionThenInvocation(t *testing.T, events <-chan Event) {
	t.Helper()
	acknowledged := false
	awaitLifecycleEvent(t, events, func(event Event) bool {
		switch value := event.(type) {
		case SubmissionSucceeded:
			if value.Raw == "/def tests /shell true" {
				acknowledged = true
			}
		case InputAccepted:
			if value.Raw == "/tests" && !acknowledged {
				t.Fatal("invocation overtook definition settlement")
			}
		case ShellCompleted:
			return true
		}
		return false
	})
}

func assertNamedRecords(t *testing.T, interp *Interpreter) {
	t.Helper()
	records := interp.Snapshot().Room.Records
	if len(records) != 4 || records[1].Text != "[defined] /tests" || records[2].Text != "/tests" {
		t.Fatalf("records=%#v", records)
	}
	content, ok := records[3].Msg.Content.(agent.Command)
	if !ok || content.Command != "/tests" {
		t.Fatalf("named display=%#v", records[3])
	}
}
