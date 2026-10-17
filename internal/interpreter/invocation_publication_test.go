package interpreter

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/roomscript/coderoom/internal/agent"
	"github.com/roomscript/coderoom/internal/interpreter/runtime"
	"github.com/roomscript/coderoom/internal/interpreter/std"
	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/promptlang"
	"github.com/roomscript/coderoom/internal/room"
	"github.com/roomscript/coderoom/internal/session"
	"github.com/roomscript/coderoom/internal/shell"
)

type publicationCommand struct{ invocation *publicationInvocation }

func (*publicationCommand) Name() string                    { return "who" }
func (*publicationCommand) Usage() string                   { return "/who" }
func (*publicationCommand) Description() string             { return "publication proof" }
func (*publicationCommand) Statement() promptlang.Statement { return promptlang.Who{} }
func (c *publicationCommand) Prepare(statement promptlang.ParsedStatement, ctx runtime.Context) (runtime.Invocation, error) {
	if ctx.Publication == nil || ctx.EventProcessing == nil || ctx.Shell == nil {
		return nil, errors.New("missing invocation capabilities")
	}
	c.invocation.ctx, c.invocation.raw = ctx, statement.Raw
	ctx.Publication.Publish([]room.Record{{Kind: room.KindSystem, Text: "during prepare"}})
	return c.invocation, nil
}

type publicationInvocation struct {
	ctx      runtime.Context
	raw      string
	mu       sync.Mutex
	result   *shell.Result
	finish   bool
	complete func(runtime.CommandResult)
	closes   atomic.Int32
}

func (*publicationInvocation) Staging() *runtime.Staging { return nil }
func (i *publicationInvocation) Go(complete func(runtime.CommandResult)) error {
	i.complete = complete
	i.ctx.Publication.Publish([]room.Record{{Kind: room.KindSystem, Text: "started"}}, LoopStatus{Message: "started"})
	// This child result belongs to the enclosing invocation, not its final result.
	if err := i.ctx.Shell.Go("condition", func(result shell.Result) {
		i.mu.Lock()
		i.result = &result
		i.mu.Unlock()
		i.ctx.EventProcessing.RequestEventProcessing()
	}); err != nil {
		return fmt.Errorf("launch condition: %w", err)
	}
	return nil
}

func (i *publicationInvocation) HandlePendingEvents() {
	i.mu.Lock()
	result, finish := i.result, i.finish
	i.result, i.finish = nil, false
	i.mu.Unlock()
	if result != nil {
		i.publishCondition(*result)
	}
	if finish {
		i.complete(runtime.CommandResult{Records: []room.Record{{Kind: room.KindSystem, Text: "finished"}}})
	}
}

func (i *publicationInvocation) publishCondition(result shell.Result) {
	output := std.FormatShellResult(result)
	record := room.NewAgentRecord(participant.Alias{Name: "shell"}, agent.Message{Mode: agent.ModeSingle, Content: agent.Command{Command: "/condition", Cwd: i.ctx.Shell.Cwd(), Output: output}})
	i.ctx.Publication.Publish([]room.Record{record}, ShellCompleted{Raw: i.raw, Statement: promptlang.ParsedStatement{Raw: "wrong source"}, Command: "/condition", Result: result, Output: output})
	i.ctx.Publication.Publish([]room.Record{{Kind: room.KindSystem, Text: "condition observed"}}, LoopStatus{Message: "condition observed"})
}

func (i *publicationInvocation) Close() {
	i.closes.Add(1)
	i.ctx.Publication.Publish([]room.Record{{Kind: room.KindSystem, Text: "during close"}})
}

func TestInvocationPublication_updatesPreserveActiveInvocation(t *testing.T) {
	events := make(chan Event, 128)
	failure := errors.New("condition failed")
	interp := New(t.Context(), session.New(), t.TempDir(), WithObserver(routingObserver{events}), WithShellRunner(ShellRunnerFunc(func(context.Context, string, string) shell.Result {
		return shell.Result{Status: shell.StatusFailure, Err: failure}
	})))
	t.Cleanup(interp.Close)
	invocation := &publicationInvocation{}
	interp.commands = runtime.NewRegistry()
	if err := interp.commands.Register(&publicationCommand{invocation: invocation}); err != nil {
		t.Fatal(err)
	}
	const raw = "  /who  "
	if err := interp.Submit(raw); err != nil {
		t.Fatal(err)
	}
	completed := awaitShellCompletion(t, events)
	assertIntermediatePublication(t, interp, invocation, completed, raw, failure)
	assertPublicationExecutionStillOpen(t, interp, invocation.ctx.Session)
	invocation.mu.Lock()
	invocation.finish = true
	invocation.mu.Unlock()
	invocation.ctx.EventProcessing.RequestEventProcessing()
	awaitLifecycleEvent(t, events, func(event Event) bool { return publicationRecordChanged(event, "finished") })
	if err := interp.fallback.Schedule(func() {
		invocation.ctx.Publication.Publish([]room.Record{{Kind: room.KindSystem, Text: "late while interpreter is open"}})
	}); err != nil {
		t.Fatal(err)
	}
	interp.Snapshot()
	interp.Close()
	assertFinalPublication(t, interp, invocation, events)
}

func assertIntermediatePublication(t *testing.T, interp *Interpreter, invocation *publicationInvocation, completed ShellCompleted, raw string, failure error) {
	t.Helper()
	statement, err := promptlang.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(completed.Statement, statement) || !errors.Is(completed.Result.Err, failure) {
		t.Fatalf("source/result=%#v", completed)
	}
	if invocation.closes.Load() != 0 {
		t.Fatal("intermediate publication closed invocation")
	}
	records := interp.Snapshot().Room.Records
	if len(records) != 4 || records[1].Text != "started" || records[3].Text != "condition observed" {
		t.Fatalf("intermediate records=%#v", records)
	}
}

func assertPublicationExecutionStillOpen(t *testing.T, interp *Interpreter, provider runtime.SessionExecutionProvider) {
	t.Helper()
	events := make(chan runtime.ExecutionEvent, 1)
	if err := interp.fallback.Schedule(func() {
		execution := provider.CreateExecution()
		var unsubscribe func()
		unsubscribe = execution.Subscribe(func(event runtime.ExecutionEvent) { events <- event; unsubscribe() })
		if err := execution.Execute(session.BroadcastCommand{Text: "probe"}); err != nil {
			t.Error(err)
		}
	}); err != nil {
		t.Fatal(err)
	}
	interp.Snapshot()
	if len(events) != 1 {
		t.Fatal("publication revoked execution capability")
	}
}

func publicationRecordChanged(event Event, text string) bool {
	changed, ok := event.(TranscriptChanged)
	return ok && slices.ContainsFunc(changed.Delta.RecordUpdates, func(update room.IndexedRecord) bool { return update.Record.Text == text })
}

func assertFinalPublication(t *testing.T, interp *Interpreter, invocation *publicationInvocation, events <-chan Event) {
	t.Helper()
	records := interp.Snapshot().Room.Records
	if len(records) != 5 || records[4].Text != "finished" || invocation.closes.Load() != 1 {
		t.Fatalf("final records=%#v, closes=%d", records, invocation.closes.Load())
	}
	// Close already ran while the lifecycle was still retained, but could not publish.
	invocation.ctx.Publication.Publish([]room.Record{{Kind: room.KindSystem, Text: "late"}})
	if len(interp.Snapshot().Room.Records) != 5 {
		t.Fatal("settled invocation published output")
	}
	assertNoShellOutcome(t, events) // Final settlement must not replay the child result.
}
