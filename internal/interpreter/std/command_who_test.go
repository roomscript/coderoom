package std_test

import (
	"slices"
	"testing"

	"github.com/roomscript/coderoom/internal/agent"
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

type whoCommandTest struct {
	name    string
	views   []participant.View
	text    string
	aliases []string
}

// whoCommandTests returns a fresh table: each test edits its views after
// preparation.
func whoCommandTests() []whoCommandTest {
	return []whoCommandTest{
		{name: "empty", text: "[no agents]"},
		{
			name:    "starting",
			views:   []participant.View{{Alias: newTestAlias("ada"), Role: "builder", Status: participant.StatusStarting}},
			text:    "[agents] ada",
			aliases: []string{"ada"},
		},
		{
			name: "multiple",
			views: []participant.View{
				{Alias: newTestAlias("tim"), Role: "reviewer", Status: participant.StatusIdle},
				{Alias: newTestAlias("ada"), Role: "builder", Status: participant.StatusWorking},
			},
			text:    "[agents] ada, tim",
			aliases: []string{"ada", "tim"},
		},
		{
			name: "with backends",
			views: []participant.View{
				{Alias: newTestAlias("tim"), Settings: agent.Settings{Backend: "codex"}},
				{Alias: newTestAlias("ada"), Settings: agent.Settings{Backend: "claude"}},
			},
			text:    "[agents] ada (claude), tim (codex)",
			aliases: []string{"ada", "tim"},
		},
	}
}

func TestWhoCommand_Go(t *testing.T) {
	for _, tt := range whoCommandTests() {
		t.Run(tt.name, func(t *testing.T) {
			invocation, err := (std.WhoCommand{}).Prepare(promptlang.ParsedStatement{Located: promptlang.Located[promptlang.Statement]{Value: promptlang.Who{}}}, runtime.Context{
				Participants: participantReader{views: tt.views},
			})
			if err != nil {
				t.Fatal(err)
			}
			for i := range tt.views {
				tt.views[i].Alias.Name = "changed after preparation"
				tt.views[i].Settings.Model = "changed after preparation"
			}
			var results []runtime.CommandResult
			err = invocation.Go(func(completion runtime.CommandResult) { results = append(results, completion) })
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
			assertWhoSnapshot(t, record.Participants, tt.aliases)
		})
	}
}

// assertWhoSnapshot checks that views are sorted by alias and unaffected by
// edits made to the reader's views after preparation.
func assertWhoSnapshot(t *testing.T, views []participant.View, aliases []string) {
	t.Helper()
	var got []string
	for _, view := range views {
		if view.Settings.Model != "" {
			t.Fatalf("snapshot shares settings with the participant reader: %#v", view)
		}
		got = append(got, view.Alias.Name)
	}
	if !slices.Equal(got, aliases) {
		t.Fatalf("snapshot aliases = %v, want %v", got, aliases)
	}
}

func TestWhoCommand_PrepareRequiresParticipantCapability(t *testing.T) {
	invocation, err := (std.WhoCommand{}).Prepare(promptlang.ParsedStatement{Located: promptlang.Located[promptlang.Statement]{Value: promptlang.Who{}}}, runtime.Context{})
	if err == nil || invocation != nil {
		t.Fatal("expected preparation failure without participant capability")
	}
}

func TestWhoCommand_PrepareCreatesIndependentInvocations(t *testing.T) {
	command := std.WhoCommand{}
	first, err := command.Prepare(promptlang.ParsedStatement{Located: promptlang.Located[promptlang.Statement]{Value: promptlang.Who{}}}, runtime.Context{Participants: participantReader{
		views: []participant.View{{Alias: newTestAlias("ada")}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	second, err := command.Prepare(promptlang.ParsedStatement{Located: promptlang.Located[promptlang.Statement]{Value: promptlang.Who{}}}, runtime.Context{Participants: participantReader{
		views: []participant.View{{Alias: newTestAlias("tim")}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	var firstRecord, secondRecord room.Record
	if err := first.Go(func(c runtime.CommandResult) { firstRecord = c.Records[0] }); err != nil {
		t.Fatal(err)
	}
	if err := second.Go(func(c runtime.CommandResult) { secondRecord = c.Records[0] }); err != nil {
		t.Fatal(err)
	}
	if firstRecord.Text != "[agents] ada" || secondRecord.Text != "[agents] tim" {
		t.Fatal("preparations did not retain independent participant listings")
	}
}

func TestWhoCommand_MetadataMatchesAST(t *testing.T) {
	command := std.WhoCommand{}
	if command.Name() != "who" || command.Description() != "list agents and their details" {
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
