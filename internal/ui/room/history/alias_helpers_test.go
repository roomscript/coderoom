package history

import (
	"sync"

	"github.com/roomscript/coderoom/internal/participant"
)

var fixtureAliases = sync.OnceValue(func() map[string]participant.Alias {
	aliases := make(map[string]participant.Alias)
	for _, name := range []string{"ada", "bob"} {
		aliases[name] = participant.New(name, "", participant.InitiativeManual).Alias
	}
	return aliases
})

func testAlias(name string) participant.Alias { return fixtureAliases()[name] }
