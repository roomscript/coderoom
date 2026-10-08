package interpreter

import (
	"slices"

	"github.com/roomscript/coderoom/internal/participant"
)

// stageRequirements owns the frozen readiness facts and later lifecycle updates.
// The workflow decides whether unmet requirements mean waiting or discarding.
type stageRequirements struct {
	participants []participantState
	unavailable  []string
}

func freezeStageRequirements(participants []participantState, routing []string) stageRequirements {
	return stageRequirements{
		participants: slices.Clone(participants),
		unavailable:  unavailableAliases(participants, routing),
	}
}

func (r *stageRequirements) waitingAliases() []string {
	return notReadyAliases(r.participants, r.unavailable)
}

func (r *stageRequirements) isReady() bool {
	return len(r.waitingAliases()) == 0
}

func (r *stageRequirements) updateStatus(alias string, status participant.Status) {
	if slices.Contains(r.unavailable, alias) {
		return
	}
	for index := range r.participants {
		if r.participants[index].alias == alias {
			if status == participant.StatusIdle && (r.participants[index].status == participant.StatusStarting || r.participants[index].status == participant.StatusAttached) {
				r.participants[index].startupPending = true
			}
			r.participants[index].status = status
			return
		}
	}
}

func (r *stageRequirements) markReady(alias string) {
	if slices.Contains(r.unavailable, alias) {
		return
	}
	for index := range r.participants {
		if r.participants[index].alias == alias {
			r.participants[index].status = participant.StatusIdle
			r.participants[index].startupPending = false
			return
		}
	}
}

func (r *stageRequirements) markUnavailable(alias string) {
	if !containsParticipant(r.participants, alias) || slices.Contains(r.unavailable, alias) {
		return
	}
	r.unavailable = append(r.unavailable, alias)
	slices.Sort(r.unavailable)
}

func (r *stageRequirements) turnID(alias string) uint64 {
	for _, value := range r.participants {
		if value.alias == alias {
			return value.turnID
		}
	}
	return 0
}

func (r *stageRequirements) updateTurnID(alias string, turnID uint64) {
	for index := range r.participants {
		if r.participants[index].alias == alias {
			r.participants[index].turnID = turnID
			return
		}
	}
}

func unavailableAliases(readinessRequirements []participantState, routing []string) []string {
	byAlias := make(map[string]participant.Status, len(readinessRequirements))
	for _, value := range readinessRequirements {
		byAlias[value.alias] = value.status
	}
	var unavailable []string
	for _, alias := range routing {
		status, ok := byAlias[alias]
		if !ok || status == participant.StatusCrashed {
			unavailable = append(unavailable, alias)
		}
	}
	slices.Sort(unavailable)
	return unavailable
}

func notReadyAliases(readinessRequirements []participantState, unavailable []string) []string {
	var blocked []string
	for _, value := range readinessRequirements {
		if !value.view().IsReadyForWork() && !slices.Contains(unavailable, value.alias) {
			blocked = append(blocked, value.alias)
		}
	}
	slices.Sort(blocked)
	return blocked
}

func containsParticipant(participants []participantState, alias string) bool {
	return slices.ContainsFunc(participants, func(value participantState) bool {
		return value.alias == alias
	})
}
