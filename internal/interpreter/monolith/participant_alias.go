package monolith

import (
	"slices"
	"strings"

	"github.com/roomscript/coderoom/internal/participant"
)

func sortParticipantAliases(aliases []participant.Alias) {
	slices.SortFunc(aliases, func(a, b participant.Alias) int { return strings.Compare(a.Name, b.Name) })
}

func participantNames(aliases []participant.Alias) []string {
	if aliases == nil {
		return nil
	}
	names := make([]string, len(aliases))
	for i, alias := range aliases {
		names[i] = alias.Name
	}
	return names
}
