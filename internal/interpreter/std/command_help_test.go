package std_test

import (
	"testing"

	"github.com/roomscript/coderoom/internal/interpreter/api"
	"github.com/roomscript/coderoom/internal/interpreter/runtime"
	"github.com/roomscript/coderoom/internal/interpreter/std"
)

func TestHelpCommand_capturesDetachedListingAndPublishesBeforeCompletion(t *testing.T) {
	listing := api.HelpListed{Commands: []api.HelpEntry{{Usage: "/test"}}, Messages: []api.HelpEntry{{Usage: "text"}}}
	publication := &loopPublication{}
	reads := 0
	command := std.HelpCommand{ReadHelp: func() api.HelpListed { reads++; return listing }}
	invocation, err := command.Prepare(parseSendStatement(t, "/help"), runtime.Context{Publication: publication})
	if err != nil {
		t.Fatal(err)
	}
	defer invocation.Close()
	listing.Commands[0].Usage, listing.Messages[0].Usage = "changed", "changed"
	if reads != 1 || len(publication.events) != 0 || invocation.Staging() != nil {
		t.Fatal("preparation did not perform only a catalogue read")
	}
	if invocation.(runtime.SubmissionAcknowledgement).ReadyToAcknowledge() {
		t.Fatal("acknowledged before help publication")
	}
	count := 0
	if err := invocation.Go(func(result runtime.CommandResult) {
		count++
		if result.Err != nil || len(publication.events) != 1 {
			t.Fatalf("completion=%#v events=%#v", result, publication.events)
		}
	}); err != nil {
		t.Fatal(err)
	}
	assertDetachedHelp(t, publication.events, count)
}

func TestHelpCommand_requiresStatementReadAndPublication(t *testing.T) {
	for _, tt := range []struct {
		raw         string
		read        func() api.HelpListed
		publication runtime.InvocationPublication
	}{
		{"/who", func() api.HelpListed { return api.HelpListed{} }, &loopPublication{}},
		{"/help", nil, &loopPublication{}},
		{"/help", func() api.HelpListed { return api.HelpListed{} }, nil},
	} {
		invocation, err := (std.HelpCommand{ReadHelp: tt.read}).Prepare(parseSendStatement(t, tt.raw), runtime.Context{Publication: tt.publication})
		if err == nil || invocation != nil {
			t.Fatalf("invocation=%#v error=%v", invocation, err)
		}
	}
}

func assertDetachedHelp(t *testing.T, events []api.Event, count int) {
	t.Helper()
	result := events[0].(api.HelpListed)
	if count != 1 || result.Commands[0].Usage != "/test" || result.Messages[0].Usage != "text" {
		t.Fatalf("listing=%#v completions=%d", result, count)
	}
}
