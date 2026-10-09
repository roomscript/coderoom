package interpreter

import (
	"reflect"
	"testing"

	"github.com/roomscript/coderoom/internal/promptlang"
)

func TestStageWorkflow_deliveryOnlyFinishesDispatchingPlan(t *testing.T) {
	for _, tc := range []struct {
		name     string
		phase    stagePhase
		finishes bool
	}{
		{"planning", stagePlanning, false},
		{"waiting", stageWaiting, false},
		{"reading source", stageReadingHandoffSource, false},
		{"dispatching", stageDispatching, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stage := stageWorkflow{}
			stage.start("@ada hello", promptlang.Send{Alias: located("ada"), Text: located("hello")})
			stage.active.phase = tc.phase
			before := *stage.active
			result := sessionOutcome{target: stage.active.pending}
			actions := stage.applySessionOutcome(result)
			if tc.finishes {
				if stage.pending() || len(actions) != 2 {
					t.Fatalf("delivery did not clear stage and report snapshot/success: %#v", actions)
				}
				if _, ok := actions[1].(publishEventInstruction).event.(SubmissionSucceeded); !ok {
					t.Fatalf("expected submission success, got %#v", actions[1])
				}
				if repeated := stage.applySessionOutcome(result); len(repeated) != 0 {
					t.Fatalf("duplicate delivery returned actions: %#v", repeated)
				}
				return
			}
			if len(actions) != 0 || !reflect.DeepEqual(*stage.active, before) {
				t.Fatal("delivery result advanced a plan that was not dispatching")
			}
		})
	}
}
