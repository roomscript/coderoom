package ui

import (
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/roomscript/coderoom/internal/interpreter"
	"github.com/roomscript/coderoom/internal/session"
	"github.com/roomscript/coderoom/internal/ui/room/history/record"
)

func TestApplicationCommands_dispatchesUIOwnedMessages(t *testing.T) {
	for _, name := range []string{"quit", "debugview", "debugrows"} {
		t.Run(name, func(t *testing.T) {
			observer := NewObserver()
			t.Cleanup(observer.Close)
			var command interpreter.ApplicationCommand
			for _, candidate := range observer.ApplicationCommands() {
				if candidate.Name == name {
					command = candidate
				}
			}
			if command.Run == nil {
				t.Fatal("UI registration missing")
			}
			if err := command.Run(); err != nil {
				t.Fatal(err)
			}
			message, ok := observer.queue.PullTimeout(time.Second)
			if !ok {
				t.Fatal("application message did not arrive")
			}
			assertDebugMessage(t, name, message)
			observer.Close()
			if err := command.Run(); err == nil {
				t.Fatal("closed UI accepted dispatch")
			}
		})
	}
}

func assertDebugMessage(t *testing.T, name string, message any) {
	t.Helper()
	switch name {
	case "quit":
		if _, ok := message.(quitRequested); !ok {
			t.Fatalf("message=%T", message)
		}
	case "debugview":
		if _, ok := message.(debugViewRequested); !ok {
			t.Fatalf("message=%T", message)
		}
	case "debugrows":
		if _, ok := message.(debugRowsRequested); !ok {
			t.Fatalf("message=%T", message)
		}
	}
}

type gatedCommandObserver struct {
	observer *Observer
	armed    atomic.Bool
	entered  chan struct{}
	release  chan struct{}
}

func (o *gatedCommandObserver) OnEvent(event interpreter.Event) {
	if o.armed.CompareAndSwap(true, false) {
		close(o.entered)
		<-o.release
	}
	o.observer.OnEvent(event)
}

func TestApplicationCommands_inputPrecedesUIResult(t *testing.T) {
	observer := NewObserver()
	t.Cleanup(observer.Close)
	gate := &gatedCommandObserver{observer: observer, entered: make(chan struct{}), release: make(chan struct{})}
	var once sync.Once
	release := func() { once.Do(func() { close(gate.release) }) }
	interp, err := interpreter.NewWithApplicationCommands(t.Context(), session.New(), t.TempDir(), observer.ApplicationCommands(), interpreter.WithObserver(gate))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(interp.Close)
	t.Cleanup(release)
	m := New(interp, observer, ".")
	initial := awaitUIMessage(observer.queue)()
	updated, _ := m.Update(initial)
	m = updated.(Model)
	gate.armed.Store(true)
	m = m.submitToInterpreter("/debugview")
	select {
	case <-gate.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("input publication did not reach observer")
	}
	progressed := make(chan struct{})
	go func() { interp.Snapshot(); close(progressed) }()
	select {
	case <-progressed:
	case <-time.After(2 * time.Second):
		t.Fatal("slow observer blocked interpreter lane")
	}
	release()
	m = processInterpreterSubmission(t, m)
	assertDebugInputBeforeResult(t, m)
}

func assertDebugInputBeforeResult(t *testing.T, m Model) {
	t.Helper()
	input, result := -1, -1
	for index, entry := range m.room.HistoryRecords() {
		if entry.Kind == record.KindUserInput && entry.Text == "/debugview" {
			input = index
		}
		if entry.Kind == record.KindSystem && strings.Contains(entry.Text, "debug commands disabled") {
			result = index
		}
	}
	if input < 0 || result <= input {
		t.Fatalf("input=%d result=%d; records=%+v", input, result, m.room.HistoryRecords())
	}
}

func TestApplicationHelpIncludesQuitWithoutDebug(t *testing.T) {
	for _, debug := range []bool{false, true} {
		m := Model{observer: NewObserver(), debug: debug}
		entries := m.applicationHelpEntries()
		m.observer.Close()
		want := 1
		if debug {
			want = 3
		}
		if len(entries) != want || entries[0].Usage != "/quit" {
			t.Fatalf("debug=%v entries=%+v", debug, entries)
		}
	}
}
