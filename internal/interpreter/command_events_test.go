package interpreter

import (
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/roomscript/coderoom/internal/interpreter/runtime"
	"github.com/roomscript/coderoom/internal/promptlang"
	"github.com/roomscript/coderoom/internal/room"
	"github.com/roomscript/coderoom/internal/session"
)

type processingCommand struct {
	invocation *processingInvocation
	prepareErr error
}

func (*processingCommand) Name() string                    { return "who" }
func (*processingCommand) Usage() string                   { return "/who" }
func (*processingCommand) Description() string             { return "event processing proof" }
func (*processingCommand) Statement() promptlang.Statement { return promptlang.Who{} }
func (c *processingCommand) Prepare(_ promptlang.ParsedStatement, ctx runtime.Context) (runtime.Invocation, error) {
	c.invocation.request = ctx.EventProcessing
	if c.invocation.request == nil {
		return nil, errors.New("event processing unavailable")
	}
	c.invocation.collect("prepare") // Requests may arrive before lifecycle registration.
	return c.invocation, c.prepareErr
}

type processingInvocation struct {
	mu              sync.Mutex
	facts           []string
	request         runtime.EventProcessingRequester
	complete        func(runtime.CommandResult)
	enteredGo       chan struct{}
	releaseGo       chan struct{}
	batches         chan []string
	completed       chan struct{}
	inGo            atomic.Bool
	processedInline atomic.Bool
	processingCalls atomic.Int32
	closes          atomic.Int32
	goErr           error
}

func (*processingInvocation) Staging() *runtime.Staging { return nil }
func (p *processingInvocation) Close()                  { p.closes.Add(1) }
func (p *processingInvocation) Go(complete func(runtime.CommandResult)) error {
	p.inGo.Store(true)
	defer p.inGo.Store(false)
	p.complete = complete
	p.collect("Go")
	close(p.enteredGo)
	<-p.releaseGo
	return p.goErr
}

// Producers only collect facts and request attention; they never drive the state.
func (p *processingInvocation) collect(fact string) {
	p.mu.Lock()
	p.facts = append(p.facts, fact)
	p.mu.Unlock()
	p.request.RequestEventProcessing()
}

func (p *processingInvocation) HandlePendingEvents() {
	p.processingCalls.Add(1)
	if p.inGo.Load() {
		p.processedInline.Store(true)
	}
	p.mu.Lock()
	facts := p.facts
	p.facts = nil
	p.mu.Unlock()
	if len(facts) == 0 {
		return
	}
	p.batches <- facts
	if slices.Contains(facts, "finish") {
		p.complete(runtime.CommandResult{Records: []room.Record{{Kind: room.KindSystem, Text: "processed events"}}})
		close(p.completed)
	}
}

func newProcessingInterpreter(t *testing.T, prepareErr, goErr error) (*Interpreter, *processingInvocation, chan Event, func()) {
	t.Helper()
	probe := &processingInvocation{enteredGo: make(chan struct{}), releaseGo: make(chan struct{}), batches: make(chan []string, 8), completed: make(chan struct{}), goErr: goErr}
	events := make(chan Event, 64)
	interp := New(t.Context(), session.New(), t.TempDir(), WithObserver(routingObserver{events}))
	t.Cleanup(interp.Close)
	var once sync.Once
	release := func() { once.Do(func() { close(probe.releaseGo) }) }
	t.Cleanup(release)
	interp.commands = runtime.NewRegistry()
	if err := interp.commands.Register(&processingCommand{invocation: probe, prepareErr: prepareErr}); err != nil {
		t.Fatal(err)
	}
	return interp, probe, events, release
}

func TestCommandEvents_interpreterProcessesCollectedFacts(t *testing.T) {
	interp, probe, _, release := newProcessingInterpreter(t, nil, nil)
	if err := interp.Submit("/who"); err != nil {
		t.Fatal(err)
	}
	awaitShellSignal(t, probe.enteredGo)
	requested := make(chan struct{})
	go func() { probe.collect("worker"); close(requested) }()
	awaitShellSignal(t, requested)
	if probe.processingCalls.Load() != 0 {
		t.Fatal("request processed events before Go returned")
	}
	release()
	assertProcessingBatch(t, probe.batches, []string{"prepare", "Go", "worker"})
	interp.Snapshot() // Drain redundant requests, which must not invent more facts.
	probe.collect("finish")
	awaitShellSignal(t, probe.completed)
	interp.Snapshot()
	assertClosedProcessingInvocation(t, interp, probe)
}

func assertProcessingBatch(t *testing.T, batches <-chan []string, want []string) {
	t.Helper()
	select {
	case facts := <-batches:
		if !slices.Equal(facts, want) {
			t.Fatalf("facts=%v, want %v", facts, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("event processing timed out")
	}
}

func assertClosedProcessingInvocation(t *testing.T, interp *Interpreter, probe *processingInvocation) {
	t.Helper()
	if probe.processedInline.Load() || probe.closes.Load() != 1 {
		t.Fatal("processing ran inline or invocation was not closed once")
	}
	records := interp.Snapshot().Room.Records
	if len(records) != 2 || records[1].Text != "processed events" {
		t.Fatalf("records=%#v", records)
	}
	calls := probe.processingCalls.Load()
	probe.collect("late")
	interp.Snapshot()
	interp.Close()
	probe.collect("after shutdown")
	if probe.processingCalls.Load() != calls || probe.closes.Load() != 1 {
		t.Fatal("stale request processed settled work")
	}
}

func TestCommandEvents_failedPreparationOrStartIgnoresRequests(t *testing.T) {
	failure := errors.New("failed")
	for _, phase := range []string{"prepare", "Go"} {
		t.Run(phase, func(t *testing.T) {
			var prepareErr, goErr error
			if phase == "prepare" {
				prepareErr = failure
			} else {
				goErr = failure
			}
			interp, probe, events, release := newProcessingInterpreter(t, prepareErr, goErr)
			release()
			if err := interp.Submit("/who"); err != nil {
				t.Fatal(err)
			}
			awaitLifecycleEvent(t, events, func(event Event) bool {
				failed, ok := event.(SubmissionFailed)
				return ok && errors.Is(failed.Err, failure)
			})
			probe.collect("late")
			interp.Snapshot()
			if probe.processingCalls.Load() != 0 || probe.closes.Load() != 1 {
				t.Fatal("failed invocation processed events or leaked resources")
			}
		})
	}
}

type eventProcessingRequestFunc func()

func (request eventProcessingRequestFunc) RequestEventProcessing() { request() }
