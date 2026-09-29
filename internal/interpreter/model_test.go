package interpreter

import (
	"testing"

	"github.com/trigosec/coderoom/internal/promptlang"
	"github.com/trigosec/coderoom/internal/session"
)

func TestInterpreterModel_SubmitBuildsInstructionsWithoutExecutingThem(t *testing.T) {
	model := newInterpreterModel()
	t.Cleanup(model.Close)

	sequence := model.Submit(
		"/invite ada",
		promptlang.Invite{Alias: "ada"},
		nil,
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
	if records := model.Snapshot().Records; len(records) != 0 {
		t.Fatalf("model executed returned instructions: records = %d", len(records))
	}
}

func TestInterpreterModel_SubmitDecidesUnknownCommand(t *testing.T) {
	model := newInterpreterModel()
	t.Cleanup(model.Close)

	sequence := model.Submit(
		"/missing",
		promptlang.CommandInvocation{Name: "missing"},
		nil,
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

	sequence := model.ApplySessionEvent(session.AgentStarted{Alias: "ada"})
	if len(sequence) != 0 {
		t.Fatalf("instructions = %d, want 0", len(sequence))
	}
	members := model.Snapshot().Members
	if len(members) != 1 || members[0] != "ada" {
		t.Fatalf("members = %v, want [ada]", members)
	}
}
