package interpreter

import (
	"testing"

	"github.com/roomscript/coderoom/internal/promptlang"
	"github.com/roomscript/coderoom/internal/session"
)

func interpreterModelOf(interpreter *Interpreter) *interpreterModel {
	return interpreter.model
}

func TestInterpreterModel_SubmitBuildsInstructionsWithoutExecutingThem(t *testing.T) {
	model := newInterpreterModel()
	t.Cleanup(model.Close)

	sequence := model.PrepareRequest(
		"/invite ada",
		promptlang.ParsedStatement{Value: promptlang.Invite{Alias: located("ada")}},
	)
	if len(sequence) != 3 {
		t.Fatalf("instructions = %d, want 3", len(sequence))
	}
	if _, ok := sequence[0].(appendRecordInstruction); !ok {
		t.Fatalf("instruction 0 = %T, want appendRecordInstruction", sequence[0])
	}
	if _, ok := sequence[1].(publishEventInstruction); !ok {
		t.Fatalf("instruction 1 = %T, want publishEventInstruction", sequence[1])
	}
	if _, ok := sequence[2].(executeCommandInstruction); !ok {
		t.Fatalf("instruction 2 = %T, want executeCommandInstruction", sequence[2])
	}
	if records := model.Snapshot().room.Records; len(records) != 0 {
		t.Fatalf("model executed returned instructions: records = %d", len(records))
	}
}

func TestInterpreterModel_SubmitDecidesUnknownCommand(t *testing.T) {
	model := newInterpreterModel()
	t.Cleanup(model.Close)

	sequence := model.PrepareRequest(
		"/missing",
		promptlang.ParsedStatement{Value: promptlang.UserCommand{Name: located("missing")}},
	)
	if len(sequence) != 1 {
		t.Fatalf("instructions = %d, want 1", len(sequence))
	}
	publish, ok := sequence[0].(publishEventInstruction)
	if !ok {
		t.Fatalf("instruction = %T, want publishEventInstruction", sequence[0])
	}
	unknown, ok := publish.event.(UnknownCommand)
	if !ok || unknown.Raw != "/missing" || unknown.Name != "missing" {
		t.Fatalf("event = %#v, want unknown /missing", publish.event)
	}
}

func TestInterpreterModel_ApplySessionEventProjectsBeforeWorkflowInstructions(t *testing.T) {
	model := newInterpreterModel()
	t.Cleanup(model.Close)

	sequence, applied := model.ApplySessionEvent(session.AgentReady{Alias: "ada"})
	if !applied {
		t.Fatal("event was not applied")
	}
	if len(sequence) != 0 {
		t.Fatalf("instructions = %d, want 0", len(sequence))
	}
	members := model.Snapshot().room.Members
	if len(members) != 1 || members[0] != "ada" {
		t.Fatalf("members = %v, want [ada]", members)
	}
}
