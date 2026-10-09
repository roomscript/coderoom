package interpreter

import (
	"slices"
	"testing"

	"github.com/roomscript/coderoom/internal/participant"
)

func TestStageRequirements_detachedFromPlanningSnapshot(t *testing.T) {
	planning := []participantState{{alias: "ada", status: participant.StatusWorking, turnID: 7}}
	requirements := freezeStageRequirements(planning, []string{"ada"})
	planning[0] = participantState{alias: "replacement", status: participant.StatusIdle, turnID: 8}
	if requirements.isReady() || !slices.Equal(requirements.waitingAliases(), []string{"ada"}) {
		t.Fatal("producer mutation changed retained requirements")
	}
	if requirements.turnID("ada") != 7 {
		t.Fatal("producer mutation changed the required turn")
	}
	requirements.updateStatus("ada", participant.StatusIdle)
	if !requirements.isReady() {
		t.Fatal("idle event did not satisfy retained requirements")
	}
	if planning[0].alias != "replacement" || planning[0].turnID != 8 {
		t.Fatal("readiness update changed the producer snapshot")
	}
}
