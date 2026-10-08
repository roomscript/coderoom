package interpreter

import (
	"reflect"
	"testing"

	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/promptlang"
)

func TestStageWorkflow_replacedSendIgnoresOldCompletions(t *testing.T) {
	for _, remove := range []string{"edit", "discard"} {
		for _, completion := range []string{"plan", "delivery"} {
			t.Run(remove+"/"+completion, func(t *testing.T) {
				workflows := workflowCollection{}
				stage := &workflows.stage
				stale := startOldSendCompletion(stage, completion)
				if remove == "edit" {
					stage.takeForEdit()
				} else {
					stage.discard()
				}
				planStageForTest(t, stage, promptlang.Send{Alias: "ben", Text: "new"},
					[]string{"ben"}, []participantState{{alias: "ben", status: participant.StatusWorking}})
				before := stage.snapshot()
				pending := stage.active.pending

				if sequence := workflows.applyCompletion(stale); len(sequence) != 0 {
					t.Fatalf("stale completion returned instructions: %#v", sequence)
				}
				if !reflect.DeepEqual(stage.snapshot(), before) || stage.active.pending != pending {
					t.Fatal("stale completion changed the replacement send")
				}
			})
		}
	}
}

func startOldSendCompletion(stage *stageWorkflow, phase string) workflowCompletion {
	stage.start("@ada old", promptlang.Send{Alias: "ada", Text: "old"})
	plan := sendPlanResult{target: stage.active.pending, targets: []string{"ada"}}
	if phase == "plan" {
		return plan
	}
	plan.participants = []participantState{{alias: "ada", status: participant.StatusIdle}}
	stage.prepareSend(plan)
	return sessionCompletion{target: stage.active.pending}
}

func TestStageWorkflow_sendPreparationIsConsumedOnce(t *testing.T) {
	for _, status := range []participant.Status{participant.StatusIdle, participant.StatusWorking} {
		t.Run(string(status), func(t *testing.T) {
			stage := stageWorkflow{}
			stage.start("@ada hello", promptlang.Send{Alias: "ada", Text: "hello"})
			plan := sendPlanResult{
				target: stage.active.pending, targets: []string{"ada"},
				participants: []participantState{{alias: "ada", status: status}},
			}
			stage.prepareSend(plan)
			before := stage.snapshot()
			pending := stage.active.pending
			if repeated := stage.prepareSend(plan); len(repeated) != 0 {
				t.Fatalf("repeated preparation returned instructions: %#v", repeated)
			}
			if !reflect.DeepEqual(stage.snapshot(), before) || stage.active.pending != pending {
				t.Fatal("repeated preparation changed the send")
			}
		})
	}
}
