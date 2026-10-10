package runtime_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/roomscript/coderoom/internal/interpreter/runtime"
	"github.com/roomscript/coderoom/internal/interpreter/std"
	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/promptlang"
	"github.com/roomscript/coderoom/internal/room"
)

type command struct {
	invocation *invocation
	err        error
}

func (*command) Statement() promptlang.Statement { return promptlang.Who{} }
func (*command) Name() string                    { return "test" }
func (*command) Usage() string                   { return "/test" }
func (*command) Description() string             { return "test command" }
func (c *command) Prepare(promptlang.ParsedStatement, runtime.Context) (runtime.Invocation, error) {
	return c.invocation, c.err
}

type invocation struct {
	complete func(runtime.Completion)
	err      error
	launches int
}

func (i *invocation) Go(complete func(runtime.Completion)) error {
	i.launches++
	i.complete = complete
	return i.err
}

func TestCommandRunner_GoReturnsBeforeCompletion(t *testing.T) {
	i := &invocation{}
	results := make(chan runtime.Completion, 1)
	err := (runtime.CommandRunner{}).Go(&command{invocation: i}, promptlang.ParsedStatement{Value: promptlang.Who{}}, runtime.Context{}, func(c runtime.Completion) { results <- c })
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-results:
		t.Fatal("completion arrived before work finished")
	default:
	}
	want := runtime.Completion{Records: []room.Record{{Kind: room.KindSystem, Text: "finished"}}}
	i.complete(want)
	if got := <-results; !reflect.DeepEqual(got, want) {
		t.Fatalf("completion = %#v, want %#v", got, want)
	}
	if i.launches != 1 {
		t.Fatalf("launches = %d, want 1", i.launches)
	}
}

func TestCommandRunner_GoFailures(t *testing.T) {
	failure := errors.New("failed")
	tests := []struct {
		name       string
		prepareErr error
		launchErr  error
		launches   int
	}{
		{name: "preparation", prepareErr: failure},
		{name: "launch", launchErr: failure, launches: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			i := &invocation{err: tt.launchErr}
			err := (runtime.CommandRunner{}).Go(&command{invocation: i, err: tt.prepareErr}, promptlang.ParsedStatement{Value: promptlang.Who{}}, runtime.Context{}, func(runtime.Completion) { t.Fatal("unexpected completion") })
			if !errors.Is(err, failure) {
				t.Fatalf("error = %v, want wrapped failure", err)
			}
			if i.launches != tt.launches {
				t.Fatalf("launches = %d, want %d", i.launches, tt.launches)
			}
		})
	}
}

type participantReader struct{}

func (participantReader) Participants() []participant.View { return []participant.View{{Alias: "ada"}} }

func TestCommandRunner_WhoCompletesImmediately(t *testing.T) {
	var results []runtime.Completion
	err := (runtime.CommandRunner{}).Go(std.WhoCommand{}, promptlang.ParsedStatement{Value: promptlang.Who{}}, runtime.Context{Participants: participantReader{}}, func(c runtime.Completion) { results = append(results, c) })
	if err != nil {
		t.Fatal(err)
	}
	want := []runtime.Completion{{Records: []room.Record{{Kind: room.KindSystem, Text: "[agents] ada"}}}}
	if !reflect.DeepEqual(results, want) {
		t.Fatalf("completions = %#v, want %#v", results, want)
	}
}
