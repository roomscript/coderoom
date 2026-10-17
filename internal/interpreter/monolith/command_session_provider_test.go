package monolith

import (
	"sync"
	"testing"

	"github.com/roomscript/coderoom/internal/interpreter/runtime"
	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/session"
)

func TestExecutionProvider_SiblingsAndSequentialDelivery(t *testing.T) {
	interp, sess, _ := newSubmitContractInterpreter(t)
	var submission *CommandSubmission
	var first, second runtime.SessionExecution
	var closeSecond func()
	events := []chan runtime.ExecutionEvent{make(chan runtime.ExecutionEvent, 2), make(chan runtime.ExecutionEvent, 2), make(chan runtime.ExecutionEvent, 2)}
	mustSubmit(t, interp.Schedule(func() {
		submission = interp.AdmitCommand("probe", parseSourceStatement(t, "first"))
		provider := submission.Context.Session
		first, second = provider.CreateExecution(), provider.CreateExecution()
		unsubscribe := first.Subscribe(func(event runtime.ExecutionEvent) { events[0] <- event })
		unsubscribe() // Closing one scope must not close its sibling or provider.
		closeSecond = second.Subscribe(func(event runtime.ExecutionEvent) { events[1] <- event })
		if err := first.Execute(providerBroadcast("closed")); err == nil {
			t.Error("closed scope admitted delivery")
		}
		if err := second.Execute(providerBroadcast("second")); err != nil {
			t.Error(err)
		}
	}))
	interp.Snapshot()
	assertProviderEvent(t, events[1])
	mustSubmit(t, interp.Schedule(func() {
		if err := second.Execute(providerBroadcast("reuse")); err == nil {
			t.Error("single-use scope admitted twice")
		}
		third := submission.Context.Session.CreateExecution()
		closeSecond()
		var unsubscribe func()
		unsubscribe = third.Subscribe(func(event runtime.ExecutionEvent) { events[2] <- event; unsubscribe() })
		if err := third.Execute(providerBroadcast("third")); err != nil {
			t.Error(err)
		}
	}))
	interp.Snapshot()
	assertProviderEvent(t, events[2])
	closeExecutionProvider(t, interp, submission)
	if len(events[0]) != 0 || len(sess.executed) != 2 {
		t.Fatal("closed scope delivered or sibling was lost")
	}
}

func closeExecutionProvider(t *testing.T, interp *Interpreter, submission *CommandSubmission) {
	t.Helper()
	mustSubmit(t, interp.Schedule(submission.Close))
	interp.Snapshot()
	submission.session.mu.Lock()
	retained := len(submission.session.executions)
	submission.session.mu.Unlock()
	if retained != 0 {
		t.Fatal("provider retained closed execution scopes")
	}
}

func TestExecutionProvider_OwnerCloseRevokesExistingAndFutureScopes(t *testing.T) {
	interp, sess, _ := newSubmitContractInterpreter(t)
	mustSubmit(t, interp.Schedule(func() {
		submission := interp.AdmitCommand("probe", parseSourceStatement(t, "work"))
		execution := submission.Context.Session.CreateExecution()
		execution.Subscribe(func(runtime.ExecutionEvent) { t.Error("revoked scope received an event") })
		if err := execution.Execute(providerBroadcast("queued")); err != nil {
			t.Error(err)
		}
		submission.Close() // Revokes queued admission before native dispatch.
		if err := execution.Execute(providerBroadcast("again")); err == nil {
			t.Error("owner close retained admission")
		}
		future := submission.Context.Session.CreateExecution()
		unsubscribe := future.Subscribe(func(runtime.ExecutionEvent) { t.Error("new scope outlived owner") })
		defer unsubscribe()
		if err := future.Execute(providerBroadcast("future")); err == nil {
			t.Error("closed provider admitted a new execution")
		}
	}))
	interp.Snapshot()
	if len(sess.executed) != 0 {
		t.Fatal("revoked execution reached Session")
	}
}

func providerBroadcast(text string) session.BroadcastCommand {
	return session.BroadcastCommand{Aliases: []participant.Alias{testAlias("ada")}, Text: text}
}

func assertProviderEvent(t *testing.T, events <-chan runtime.ExecutionEvent) {
	t.Helper()
	if len(events) != 1 {
		t.Fatalf("terminal events=%d", len(events))
	}
	result, ok := (<-events).(runtime.ExecutionCompleted)
	if !ok || result.Result.Kind != session.RoutingBroadcast {
		t.Fatalf("terminal=%#v", result)
	}
}

func TestExecutionProvider_ConcurrentScopeCreationAndClose(t *testing.T) {
	interp, _, _ := newSubmitContractInterpreter(t)
	provider := &commandSessionProvider{executor: interp.executor}
	var workers sync.WaitGroup
	for range 20 {
		workers.Go(func() {
			execution := provider.CreateExecution()
			execution.Subscribe(func(runtime.ExecutionEvent) { t.Error("unexpected event") })()
		})
	}
	workers.Go(provider.close)
	workers.Wait()
	if len(provider.executions) != 0 || !provider.closed {
		t.Fatal("closed provider retained execution scopes")
	}
}

func TestExecutionProvider_ScopesReceiveOnlyTheirOwnOutcome(t *testing.T) {
	sess := &identifiedDeliverySession{submitContractSession: newSubmitContractSession()}
	sess.next.Store(40)
	interp := New(t.Context(), sess, t.TempDir())
	t.Cleanup(interp.Close)
	events := []chan runtime.ExecutionEvent{make(chan runtime.ExecutionEvent, 2), make(chan runtime.ExecutionEvent, 2)}
	var submission *CommandSubmission
	mustSubmit(t, interp.Schedule(func() {
		submission = interp.AdmitCommand("probe", parseSourceStatement(t, "work"))
		for index := range events {
			execution := submission.Context.Session.CreateExecution()
			execution.Subscribe(func(event runtime.ExecutionEvent) { events[index] <- event })
			if err := execution.Execute(providerBroadcast("identical recipients")); err != nil {
				t.Error(err)
			}
		}
	}))
	interp.Snapshot()
	for index, stream := range events {
		if len(stream) != 1 {
			t.Fatalf("scope %d received %d events", index, len(stream))
		}
		outcome, ok := (<-stream).(runtime.ExecutionCompleted)
		if !ok || outcome.ExecutionID != session.ExecutionID(41+index) {
			t.Fatalf("scope %d received another execution: %#v", index, outcome)
		}
	}
	closeExecutionProvider(t, interp, submission)
}
