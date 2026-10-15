package interpreter

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/roomscript/coderoom/internal/interpreter/runtime"
	"github.com/roomscript/coderoom/internal/promptlang"
	"github.com/roomscript/coderoom/internal/room"
	"github.com/roomscript/coderoom/internal/session"
)

type lifecycleProbe struct {
	prepareErr error
	launchErr  error
	immediate  bool
	callbacks  chan func(runtime.CommandResult)
	closes     atomic.Int32
}

func (*lifecycleProbe) Name() string                    { return "who" }
func (*lifecycleProbe) Usage() string                   { return "/who" }
func (*lifecycleProbe) Description() string             { return "lifecycle ownership probe" }
func (*lifecycleProbe) Statement() promptlang.Statement { return promptlang.Who{} }
func (p *lifecycleProbe) Prepare(promptlang.ParsedStatement, runtime.Context) (runtime.Invocation, error) {
	return p, p.prepareErr
}
func (*lifecycleProbe) Staging() *runtime.Staging { return nil }
func (p *lifecycleProbe) Close()                  { p.closes.Add(1) }
func (p *lifecycleProbe) Go(complete func(runtime.CommandResult)) error {
	p.callbacks <- complete
	if p.immediate {
		complete(probeCompletion())
		complete(probeCompletion())
	}
	return p.launchErr
}
func probeCompletion() runtime.CommandResult {
	return runtime.CommandResult{Records: []room.Record{{Kind: room.KindSystem, Text: "probe completed"}}}
}

func newProbeInterpreter(ctx context.Context, t *testing.T, probe *lifecycleProbe) (*Interpreter, chan Event) {
	t.Helper()
	events := make(chan Event, 64)
	interp := New(ctx, session.New(), t.TempDir(), WithObserver(routingObserver{events}))
	t.Cleanup(interp.Close)
	interp.commands = runtime.NewRegistry()
	if err := interp.commands.Register(probe); err != nil {
		t.Fatal(err)
	}
	return interp, events
}

func TestCommandLifecycle_settlementOwnership(t *testing.T) {
	for _, scenario := range []string{"prepare failure", "launch failure", "immediate", "delayed", "unfinished"} {
		t.Run(scenario, func(t *testing.T) {
			cause := errors.New("probe failure")
			probe := createLifecycleProbe(scenario, cause)
			interp, events := newProbeInterpreter(context.Background(), t, probe)
			if err := interp.Submit("/who"); err != nil {
				t.Fatal(err)
			}
			awaitProbeLaunch(t, events, scenario, cause)
			if scenario == "delayed" {
				complete := <-probe.callbacks
				complete(probeCompletion())
				complete(probeCompletion())
			}
			interp.Close()
			assertProbeSettlement(t, interp, probe, scenario)
			if scenario != "prepare failure" {
				if scenario != "delayed" {
					(<-probe.callbacks)(probeCompletion())
				}
			}
			if err := interp.Submit("/who"); !errors.Is(err, ErrClosed) {
				t.Fatalf("late submission = %v", err)
			}
			assertProbeSettlement(t, interp, probe, scenario)
		})
	}
}

func createLifecycleProbe(scenario string, cause error) *lifecycleProbe {
	probe := &lifecycleProbe{callbacks: make(chan func(runtime.CommandResult), 1)}
	switch scenario {
	case "prepare failure":
		probe.prepareErr = cause
	case "launch failure":
		probe.launchErr, probe.immediate = cause, true
	case "immediate":
		probe.immediate = true
	}
	return probe
}

func awaitProbeLaunch(t *testing.T, events <-chan Event, scenario string, cause error) {
	t.Helper()
	timeout := time.After(2 * time.Second)
	for {
		select {
		case event := <-events:
			switch event := event.(type) {
			case SubmissionSucceeded:
				if scenario == "prepare failure" || scenario == "launch failure" {
					t.Fatal("failed command reported success")
				}
				assertRoutingSource(t, event.Statement)
				return
			case SubmissionFailed:
				if !errors.Is(event.Err, cause) {
					t.Fatalf("failure = %v", event.Err)
				}
				assertRoutingSource(t, event.Statement)
				return
			}
		case <-timeout:
			t.Fatal("timed out waiting for probe launch")
			return
		}
	}
}

func assertProbeSettlement(t *testing.T, interp *Interpreter, probe *lifecycleProbe, scenario string) {
	t.Helper()
	if len(interp.invocations) != 0 {
		t.Fatal("new interpreter retained a lifecycle")
	}
	if probe.closes.Load() != 1 {
		t.Fatalf("invocation close calls = %d", probe.closes.Load())
	}
	records := interp.Snapshot().Room.Records
	want := 1
	if scenario == "immediate" || scenario == "delayed" {
		want = 2
	}
	if len(records) != want || records[0].Text != "/who" {
		t.Fatalf("records = %#v", records)
	}
	if want == 2 && records[1].Text != "probe completed" {
		t.Fatalf("completion = %#v", records[1])
	}
}

func TestCommandLifecycle_contextCancellationClosesOwnedInvocations(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	probe := &lifecycleProbe{callbacks: make(chan func(runtime.CommandResult), 1)}
	interp, events := newProbeInterpreter(ctx, t, probe)
	if err := interp.Submit("/who"); err != nil {
		t.Fatal(err)
	}
	awaitProbeLaunch(t, events, "unfinished", nil)
	cancel()
	awaitLifecycleEvent(t, events, func(event Event) bool {
		failure, ok := event.(OperationFailed)
		return ok && errors.Is(failure.Err, ErrClosed)
	})
	interp.Close()
	assertProbeSettlement(t, interp, probe, "unfinished")
}
