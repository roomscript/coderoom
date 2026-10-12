package interpreter

import (
	"slices"
	"testing"

	"github.com/roomscript/coderoom/internal/participant"
)

func TestStageRequirements_detachedFromPlanningSnapshot(t *testing.T) {
	planning := []participantState{{alias: testAlias("ada"), status: participant.StatusWorking, turnID: 7}}
	requirements := freezeStageRequirements(planning, testAliases([]string{"ada"}))
	planning[0] = participantState{alias: testAlias("replacement"), status: participant.StatusIdle, turnID: 8}
	if requirements.isReady() || !slices.Equal(requirements.waitingAliases(), testAliases([]string{"ada"})) {
		t.Fatal("producer mutation changed retained requirements")
	}
	if requirements.turnID(testAlias("ada")) != 7 {
		t.Fatal("producer mutation changed the required turn")
	}
	requirements.updateStatus(testAlias("ada"), participant.StatusIdle)
	if !requirements.isReady() {
		t.Fatal("idle event did not satisfy retained requirements")
	}
	if planning[0].alias != testAlias("replacement") || planning[0].turnID != 8 {
		t.Fatal("readiness update changed the producer snapshot")
	}
}
