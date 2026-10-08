package interpreter

import (
	"reflect"
	"testing"

	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/promptlang"
)

func TestStageWorkflow_broadcastPreparationIsConsumedOnce(t *testing.T) {
	for _, status := range []participant.Status{participant.StatusIdle, participant.StatusWorking} {
		t.Run(string(status), func(t *testing.T) {
			stage := stageWorkflow{}
			stage.start("hello", promptlang.Broadcast{Text: "hello"})
			selection := broadcastPlanResult{target: stage.active.pending, targets: []string{"ada"}}
			stage.handleBroadcastPlan(selection)
			readiness := participantStateResult{
				target:                stage.active.pending,
				readinessRequirements: []participantState{{alias: "ada", status: status}},
			}
			stage.handleParticipantState(readiness)
			before := stage.snapshot()
			pending := stage.active.pending
			if actions := stage.handleBroadcastPlan(selection); len(actions) != 0 {
				t.Fatalf("repeated selection returned actions: %#v", actions)
			}
			if actions := stage.handleParticipantState(readiness); len(actions) != 0 {
				t.Fatalf("repeated readiness returned actions: %#v", actions)
			}
			if !reflect.DeepEqual(stage.snapshot(), before) || stage.active.pending != pending {
				t.Fatal("repeated preparation changed the broadcast")
			}
		})
	}
}
