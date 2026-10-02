package interpreter

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/trigosec/coderoom/internal/session"
)

func TestExecuteLegacy_waitsForExecutionAndReturnsItsError(t *testing.T) {
	wantErr := errors.New("execute failed")
	interp, sess, _ := newSubmitContractInterpreter(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	sess.executeErr = wantErr
	sess.execute = func(session.Command, session.Observer) {
		close(entered)
		<-release
	}

	result := executeLegacyAsync(interp, session.CancelCommand{Alias: "ada"})
	receiveSignal(t, entered, "legacy Execute")
	assertNoSubmitResult(t, result, "ExecuteLegacy returned before execution completed")
	close(release)

	if err := receiveSubmitResult(t, result); !errors.Is(err, wantErr) {
		t.Fatalf("ExecuteLegacy error = %v, want %v", err, wantErr)
	}
}

func TestExecuteLegacy_projectsCausalEventsBeforeReturning(t *testing.T) {
	interp, sess, _ := newSubmitContractInterpreter(t)
	sess.execute = func(_ session.Command, observer session.Observer) {
		observer.OnEvent(session.AgentStarted{Alias: "ada"})
	}

	if err := interp.ExecuteLegacy(session.CancelCommand{Alias: "ada"}); err != nil {
		t.Fatalf("ExecuteLegacy: %v", err)
	}

	members := interpreterModelOf(interp).Snapshot().room.Members
	if len(members) != 1 || members[0] != "ada" {
		t.Fatalf("projected members = %v, want [ada]", members)
	}
}

func TestExecuteLegacy_serializesWithSubmissions(t *testing.T) {
	interp, sess, _ := newSubmitContractInterpreter(t)
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	sess.execute = func(session.Command, session.Observer) {
		entered <- struct{}{}
		<-release
	}

	legacyResult := executeLegacyAsync(interp, session.CancelCommand{Alias: "ada"})
	receiveSignal(t, entered, "legacy Execute")
	mustSubmit(t, interp.Submit("/cancel bob"))
	assertNoSignal(t, entered, "submission executed while legacy command was active")

	release <- struct{}{}
	if err := receiveSubmitResult(t, legacyResult); err != nil {
		t.Fatalf("ExecuteLegacy: %v", err)
	}
	receiveSignal(t, entered, "native Execute")
	release <- struct{}{}
	if maximum := sess.maxActive.Load(); maximum != 1 {
		t.Fatalf("maximum concurrent Execute calls = %d, want 1", maximum)
	}
}

func TestExecuteLegacy_serializesConcurrentCallers(t *testing.T) {
	interp, sess, _ := newSubmitContractInterpreter(t)

	const executions = 20
	var callers sync.WaitGroup
	results := make(chan error, executions)
	callers.Add(executions)
	for range executions {
		go func() {
			defer callers.Done()
			results <- interp.ExecuteLegacy(session.CancelCommand{Alias: "ada"})
		}()
	}
	callers.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatalf("ExecuteLegacy: %v", err)
		}
	}
	if maximum := sess.maxActive.Load(); maximum != 1 {
		t.Fatalf("maximum concurrent Execute calls = %d, want 1", maximum)
	}
}

func TestExecuteLegacy_rejectsAfterShutdown(t *testing.T) {
	sess := newSubmitContractSession()
	interp := New(context.Background(), sess, t.TempDir())
	interp.Close()

	err := interp.ExecuteLegacy(session.CancelCommand{Alias: "ada"})
	if !errors.Is(err, ErrClosed) {
		t.Fatalf("ExecuteLegacy error = %v, want ErrClosed", err)
	}
}

func TestExecuteLegacy_acceptedCallCompletesDuringShutdown(t *testing.T) {
	sess := newSubmitContractSession()
	interp := New(context.Background(), sess, t.TempDir())
	entered := make(chan struct{})
	release := make(chan struct{})
	sess.execute = func(session.Command, session.Observer) {
		close(entered)
		<-release
	}

	result := executeLegacyAsync(interp, session.CancelCommand{Alias: "ada"})
	receiveSignal(t, entered, "legacy Execute")
	closed := make(chan struct{})
	go func() {
		interp.Close()
		close(closed)
	}()
	assertNoSignal(t, closed, "Close returned while accepted execution was active")
	close(release)

	if err := receiveSubmitResult(t, result); err != nil {
		t.Fatalf("ExecuteLegacy: %v", err)
	}
	receiveSignal(t, closed, "Close")
}

func executeLegacyAsync(i *Interpreter, command session.Command) <-chan error {
	result := make(chan error, 1)
	go func() {
		result <- i.ExecuteLegacy(command)
	}()
	return result
}

func assertNoSubmitResult(t *testing.T, results <-chan error, description string) {
	t.Helper()
	select {
	case err := <-results:
		t.Fatalf("%s: %v", description, err)
	case <-time.After(20 * time.Millisecond):
	}
}
