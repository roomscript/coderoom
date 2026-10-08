package interpreter

import (
	"errors"
	"testing"

	"github.com/roomscript/coderoom/internal/promptlang"
)

func TestStageWorkflow_unsupportedActionDoesNotReplaceWork(t *testing.T) {
	for _, tc := range []struct {
		name    string
		pending bool
	}{{name: "empty"}, {name: "pending", pending: true}} {
		t.Run(tc.name, func(t *testing.T) {
			stage := stageWorkflow{}
			if tc.pending {
				stage.start("@ada hello", promptlang.Send{Alias: "ada", Text: "hello"})
			}
			before, generation, requestID := stage.active, stage.nextGeneration, stage.nextRequestID
			actions := stage.start("/who", promptlang.Who{})
			if len(actions) != 1 {
				t.Fatalf("rejection actions = %#v", actions)
			}
			failed, ok := actions[0].(publishEventInstruction).event.(SubmissionFailed)
			if !ok || !errors.Is(failed.Err, errUnsupportedStageAction) || failed.Raw != "/who" {
				t.Fatalf("rejection = %#v", actions)
			}
			if stage.active != before || stage.nextGeneration != generation || stage.nextRequestID != requestID {
				t.Fatal("unsupported action changed current work")
			}
		})
	}
}
