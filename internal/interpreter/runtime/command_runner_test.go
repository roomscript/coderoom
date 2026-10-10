package runtime_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/roomscript/coderoom/internal/interpreter/runtime"
	"github.com/roomscript/coderoom/internal/interpreter/std"
	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/room"
)

type command struct {
	invocation *invocation
	err        error
	prepares   int
}

func (*command) Name() string        { return "test" }
func (*command) Usage() string       { return "/test" }
func (*command) Description() string { return "test command" }
func (c *command) Prepare(runtime.Context) (runtime.Invocation, error) {
	c.prepares++
	return c.invocation, c.err
}

type invocation struct {
	steps []runtime.Step
	inits int
	nexts int
}

func (i *invocation) Init() { i.inits++ }
func (i *invocation) Next() runtime.Step {
	if i.inits != 1 {
		panic("Next called without exactly one Init")
	}
	step := i.steps[i.nexts]
	i.nexts++
	return step
}

func TestCommandRunner_Run(t *testing.T) {
	first := room.Record{Kind: room.KindSystem, Text: "first"}
	last := room.Record{Kind: room.KindSystem, Text: "last"}
	tests := []struct {
		name    string
		steps   []runtime.Step
		want    []room.Record
		wantErr bool
	}{
		{name: "empty completion", steps: []runtime.Step{{Done: true}}},
		{
			name:  "final record",
			steps: []runtime.Step{{Records: []room.Record{last}, Done: true}},
			want:  []room.Record{last},
		},
		{
			name: "ordered record across steps",
			steps: []runtime.Step{
				{Records: []room.Record{first}},
				{Records: []room.Record{last}, Done: true},
			},
			want: []room.Record{first, last},
		},
		{name: "pending without polling", steps: []runtime.Step{{}}, wantErr: true},
		{
			name:  "pending retains prior record",
			steps: []runtime.Step{{Records: []room.Record{first}}, {}},
			want:  []room.Record{first}, wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			i := &invocation{steps: tt.steps}
			c := &command{invocation: i}
			got, err := (runtime.CommandRunner{}).Run(c, runtime.Context{})
			if (err != nil) != tt.wantErr {
				t.Fatalf("Run error = %v, want error %v", err, tt.wantErr)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("records = %#v, want %#v", got, tt.want)
			}
			if c.prepares != 1 || i.inits != 1 || i.nexts != len(tt.steps) {
				t.Fatalf("prepare/init/next counts = %d/%d/%d", c.prepares, i.inits, i.nexts)
			}
		})
	}
}

func TestCommandRunner_PreparationFailureDoesNotInitialize(t *testing.T) {
	i := &invocation{}
	c := &command{invocation: i, err: errors.New("preparation failed")}
	records, err := (runtime.CommandRunner{}).Run(c, runtime.Context{})
	if err == nil || len(records) != 0 || i.inits != 0 || i.nexts != 0 {
		t.Fatal("preparation failure did not stop before initialization")
	}
}

type participantReader struct{}

func (participantReader) Participants() []participant.View {
	return []participant.View{{Alias: "ada", Role: "builder", Status: participant.StatusIdle}}
}

func TestCommandRunner_Who(t *testing.T) {
	records, err := (runtime.CommandRunner{}).Run(std.WhoCommand{}, runtime.Context{
		Participants: participantReader{},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []room.Record{{Kind: room.KindSystem, Text: "[agents] ada"}}
	if !reflect.DeepEqual(records, want) {
		t.Fatalf("records = %#v, want %#v", records, want)
	}
}
