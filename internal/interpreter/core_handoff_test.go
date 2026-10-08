package interpreter

import (
	"reflect"
	"testing"

	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/promptlang"
)

func TestStageWorkflow_handoffPreparationIsConsumedOnce(t *testing.T) {
	for _, status := range []participant.Status{participant.StatusIdle, participant.StatusWorking} {
		t.Run(string(status), func(t *testing.T) {
			stage := stageWorkflow{}
			stage.start("/handoff ada ben", promptlang.Handoff{FromAlias: "ada", ToAlias: "ben"})
			facts := participantStateResult{
				target: stage.active.pending,
				readinessRequirements: []participantState{
					{alias: "ada", status: status}, {alias: "ben", status: participant.StatusIdle},
				},
			}
			stage.handleParticipantState(facts)
			before := *stage.active
			snapshot := stage.snapshot()
			if actions := stage.handleParticipantState(facts); len(actions) != 0 {
				t.Fatalf("repeated preparation returned actions: %#v", actions)
			}
			if !reflect.DeepEqual(*stage.active, before) || !reflect.DeepEqual(stage.snapshot(), snapshot) {
				t.Fatal("repeated preparation changed the handoff")
			}
		})
	}
}
