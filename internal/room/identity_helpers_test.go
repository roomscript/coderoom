package room

import "github.com/roomscript/coderoom/internal/participant"

func newTestAlias(name string) participant.Alias {
	return participant.New(name, "builder", participant.InitiativeManual).Alias
}
