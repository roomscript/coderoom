package interpreter_test

import (
	"reflect"
	"testing"

	"github.com/roomscript/coderoom/internal/interpreter"
)

func TestHelp_publishesCatalogueBeforeSubmissionCompletion(t *testing.T) {
	interp, sess, events := newSubmitExample()
	defer interp.Close()
	if err := interp.Submit("/help"); err != nil {
		t.Fatal(err)
	}
	accepted := receiveEvent[interpreter.InputAccepted](t, events)
	if accepted.Raw != "/help" {
		t.Fatalf("acceptance=%#v", accepted)
	}
	changed := receiveEvent[interpreter.StateChanged](t, events)
	if len(changed.Snapshot.Room.Records) != 1 || changed.Snapshot.Room.Records[0].Text != "/help" {
		t.Fatalf("records=%#v", changed.Snapshot.Room.Records)
	}
	listing := receiveEvent[interpreter.HelpListed](t, events)
	var usages []string
	for _, entry := range listing.Commands {
		usages = append(usages, entry.Usage)
	}
	want := []string{
		"/policy enable <name>", "/invite <alias>",
		"/remove <alias>", "/cancel <alias>", "/handoff <from> <to>", "/shell <program>",
		"/def <name> /shell <program>", "/<name>",
		"/loop @<alias> <prompt> /until /<name> /max <turns>", "/who", "/help", "@<alias> <text>", "<text>",
	}
	if !reflect.DeepEqual(usages, want) {
		t.Fatalf("command usages=%v", usages)
	}
	assertHelpSubmissionOutcome(t, events)
	interp.Close()
	assertSessionCommandSettled(t, sess, events)
}

func assertHelpSubmissionOutcome(t *testing.T, events <-chan interpreter.Event) {
	t.Helper()
	for {
		switch event := receiveEvent[interpreter.Event](t, events).(type) {
		case interpreter.StateChanged:
			if len(event.Snapshot.Room.Records) != 1 || event.Snapshot.Room.Records[0].Text != "/help" {
				t.Fatalf("records=%#v", event.Snapshot.Room.Records)
			}
		case interpreter.SubmissionSucceeded:
			if event.Raw != "/help" {
				t.Fatalf("success=%#v", event)
			}
			return
		default:
			t.Fatalf("unexpected event before help success=%#v", event)
		}
	}
}
