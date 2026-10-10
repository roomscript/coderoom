package std_test

import (
	"testing"

	"github.com/roomscript/coderoom/internal/interpreter/runtime"
	"github.com/roomscript/coderoom/internal/interpreter/std"
	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/promptlang"
	"github.com/roomscript/coderoom/internal/room"
)

type participantReader struct {
	views []participant.View
}

func (r participantReader) Participants() []participant.View { return r.views }

func TestWhoCommand_Go(t *testing.T) {
	tests := []struct {
		name  string
		views []participant.View
		text  string
	}{
		{name: "empty", text: "[no agents]"},
		{
			name:  "starting",
			views: []participant.View{{Alias: "ada", Role: "builder", Status: participant.StatusStarting}},
			text:  "[agents] ada",
		},
		{
			name: "multiple",
			views: []participant.View{
				{Alias: "tim", Role: "reviewer", Status: participant.StatusIdle},
				{Alias: "ada", Role: "builder", Status: participant.StatusWorking},
			},
			text: "[agents] ada, tim",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			invocation, err := (std.WhoCommand{}).Prepare(runtime.Context{
				Participants: participantReader{views: tt.views},
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(tt.views) > 0 {
				tt.views[0].Alias = "changed after preparation"
			}
			var results []runtime.Completion
			err = invocation.Go(func(completion runtime.Completion) { results = append(results, completion) })
			if err != nil {
				t.Fatal(err)
			}
			if len(results) != 1 || results[0].Err != nil || len(results[0].Records) != 1 {
				t.Fatalf("completions = %#v, want one system record", results)
			}
			record := results[0].Records[0]
			want := room.Record{Kind: room.KindSystem, Text: tt.text}
			if record.Kind != want.Kind || record.Text != want.Text {
				t.Fatalf("record = %#v, want %#v", record, want)
			}
		})
	}
}

func TestWhoCommand_PrepareRequiresParticipantCapability(t *testing.T) {
	invocation, err := (std.WhoCommand{}).Prepare(runtime.Context{})
	if err == nil || invocation != nil {
		t.Fatal("expected preparation failure without participant capability")
	}
}

func TestWhoCommand_PrepareCreatesIndependentInvocations(t *testing.T) {
	command := std.WhoCommand{}
	first, err := command.Prepare(runtime.Context{Participants: participantReader{
		views: []participant.View{{Alias: "ada"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	second, err := command.Prepare(runtime.Context{Participants: participantReader{
		views: []participant.View{{Alias: "tim"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	var firstRecord, secondRecord room.Record
	if err := first.Go(func(c runtime.Completion) { firstRecord = c.Records[0] }); err != nil {
		t.Fatal(err)
	}
	if err := second.Go(func(c runtime.Completion) { secondRecord = c.Records[0] }); err != nil {
		t.Fatal(err)
	}
	if firstRecord.Text != "[agents] ada" || secondRecord.Text != "[agents] tim" {
		t.Fatal("preparations did not retain independent participant listings")
	}
}

func TestWhoCommand_MetadataMatchesAST(t *testing.T) {
	command := std.WhoCommand{}
	if command.Name() != "who" || command.Description() != "list agents" {
		t.Fatal("unexpected registration metadata")
	}
	parsed, err := promptlang.Parse(command.Usage())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := parsed.Value.(promptlang.Who); !ok {
		t.Fatalf("help example parsed as %T, want Who", parsed.Value)
	}
}
