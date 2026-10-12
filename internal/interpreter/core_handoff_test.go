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
			stage.start("/handoff ada ben", promptlang.Handoff{FromAlias: located("ada"), ToAlias: located("ben")})
			facts := handoffPlanResult{
				from: testAlias("ada"), to: testAlias("ben"),
				target: stage.active.pending,
				participants: []participantState{
					{alias: testAlias("ada"), status: status}, {alias: testAlias("ben"), status: participant.StatusIdle},
				},
			}
			stage.prepareHandoff(facts)
			before := *stage.active
			snapshot := stage.snapshot()
			if actions := stage.prepareHandoff(facts); len(actions) != 0 {
				t.Fatalf("repeated preparation returned actions: %#v", actions)
			}
			if !reflect.DeepEqual(*stage.active, before) || !reflect.DeepEqual(stage.snapshot(), snapshot) {
				t.Fatal("repeated preparation changed the handoff")
			}
		})
	}
}
