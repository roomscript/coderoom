package interpreter

import (
	"context"
	"testing"

	"github.com/roomscript/coderoom/internal/interpreter/runtime"
	"github.com/roomscript/coderoom/internal/promptlang"
	"github.com/roomscript/coderoom/internal/room"
)

type registeredTestCommand struct{ *callbackTestCommand }

func (registeredTestCommand) Name() string                    { return "proof" }
func (registeredTestCommand) Usage() string                   { return "/help" }
func (registeredTestCommand) Description() string             { return "registered proof" }
func (registeredTestCommand) Statement() promptlang.Statement { return promptlang.Help{} }

func TestRegisteredCommand_UsesSharedDispatchAndHelp(t *testing.T) {
	model := newInterpreterModel()
	completion := runtime.Completion{Records: []room.Record{{Kind: room.KindSystem, Text: "registered output"}}}
	command := registeredTestCommand{&callbackTestCommand{
		callbacks: make(chan func(runtime.Completion), 1), immediate: &completion,
	}}
	if err := model.modules.Register(command); err != nil {
		t.Fatal(err)
	}
	listing := model.helpListing()
	last := listing.Commands[len(listing.Commands)-1]
	if last.Usage != command.Usage() || last.Description != command.Description() {
		t.Fatalf("help entry = %#v", last)
	}
	events := make(chan Event, 32)
	executor := newInterpreterExecutor(context.Background(), newSubmitContractSession(), t.TempDir(), model)
	executor.dispatcher.addObserver(transcriptTestObserver{events: events})
	executor.start()
	t.Cleanup(func() { executor.requestClose(); <-executor.done })
	receiveSubmitEvent[StateChanged](t, events)
	if !executor.enqueue(submitOperation{raw: "/help"}) {
		t.Fatal("could not enqueue input")
	}
	receiveSubmitEvent[InputAccepted](t, events)
	assertCommandTranscript(t, events, "/help", "registered output")
	receiveSubmitEvent[StateChanged](t, events)
	succeeded := receiveSubmitEvent[SubmissionSucceeded](t, events)
	if _, ok := succeeded.Statement.Value.(promptlang.Help); !ok {
		t.Fatalf("source = %#v", succeeded.Statement)
	}
	assertNoSubmitEvent(t, events)
}
