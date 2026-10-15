package interpreter

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/roomscript/coderoom/internal/interpreter/runtime"
	"github.com/roomscript/coderoom/internal/promptlang"
	"github.com/roomscript/coderoom/internal/room"
	"github.com/roomscript/coderoom/internal/session"
)

type routingProbe struct {
	preparations atomic.Int32
	readers      atomic.Int32
	launches     atomic.Int32
	closes       atomic.Int32
}

func (*routingProbe) Name() string                    { return "who" }
func (*routingProbe) Usage() string                   { return "/who" }
func (*routingProbe) Description() string             { return "routing proof" }
func (*routingProbe) Statement() promptlang.Statement { return promptlang.Who{} }
func (p *routingProbe) Prepare(_ promptlang.ParsedStatement, ctx runtime.Context) (runtime.Invocation, error) {
	p.preparations.Add(1)
	if ctx.Participants != nil {
		ctx.Participants.Participants()
		p.readers.Add(1)
	}
	return p, nil
}
func (*routingProbe) Staging() *runtime.Staging { return nil }
func (p *routingProbe) Close()                  { p.closes.Add(1) }
func (p *routingProbe) Go(complete func(runtime.CommandResult)) error {
	p.launches.Add(1)
	result := runtime.CommandResult{Records: []room.Record{{Kind: room.KindSystem, Text: "modular who proof"}}}
	complete(result)
	complete(result) // The shared coordinator must consume completion only once.
	return nil
}

type routingObserver struct{ events chan Event }

func (o routingObserver) OnEvent(event Event) { o.events <- event }

func TestSubmit_ModularSelectionUsesSharedCoordinatorOnce(t *testing.T) {
	events := make(chan Event, 64)
	interp := New(context.Background(), session.New(), t.TempDir(), WithObserver(routingObserver{events: events}))
	t.Cleanup(interp.Close)
	probe := &routingProbe{}
	interp.commands = runtime.NewRegistry()
	if err := interp.commands.Register(probe); err != nil {
		t.Fatal(err)
	}
	if err := interp.Submit("/who"); err != nil {
		t.Fatal(err)
	}
	interp.Close() // Accepted command and queued completion must survive shutdown.
	if probe.preparations.Load() != 1 || probe.readers.Load() != 1 || probe.launches.Load() != 1 || probe.closes.Load() != 1 {
		t.Fatal("selected command did not prepare, launch and close once with monolith participant reads")
	}
	records := interp.Snapshot().Room.Records
	if len(records) != 2 || records[0].Text != "/who" || records[1].Text != "modular who proof" {
		t.Fatalf("canonical records = %#v; want one input and one modular result", records)
	}
	assertRoutingPublication(t, events)
}

func assertRoutingPublication(t *testing.T, events <-chan Event) {
	t.Helper()
	accepted, succeeded, notices := 0, 0, 0
	for len(events) != 0 {
		switch event := (<-events).(type) {
		case InputAccepted:
			accepted++
		case SubmissionSucceeded:
			succeeded++
			assertRoutingSource(t, event.Statement)
		case TranscriptChanged:
			notices += countRoutingNotices(t, event.Delta, succeeded)
		case OperationFailed, SubmissionFailed:
			t.Fatalf("unexpected modular failure: %#v", event)
		}
	}
	if accepted != 1 || succeeded != 1 || notices != 1 {
		t.Fatalf("publications: accepted=%d, succeeded=%d, notices=%d", accepted, succeeded, notices)
	}
}

func assertRoutingSource(t *testing.T, statement promptlang.ParsedStatement) {
	t.Helper()
	if _, ok := statement.Value.(promptlang.Who); !ok || statement.Raw != "/who" {
		t.Fatal("modular acknowledgement lost parsed source")
	}
}

func countRoutingNotices(t *testing.T, delta room.Delta, succeeded int) int {
	t.Helper()
	count := 0
	for _, update := range delta.RecordUpdates {
		if update.Record.Text != "modular who proof" {
			continue
		}
		count++
		if succeeded != 1 {
			t.Fatal("completion notice preceded launch acknowledgement")
		}
	}
	return count
}
