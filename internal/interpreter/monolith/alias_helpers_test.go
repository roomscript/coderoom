package monolith

import (
	"sync"

	"github.com/roomscript/coderoom/internal/participant"
)

var aliasFixtures = sync.OnceValue(func() map[string]participant.Alias {
	aliases := make(map[string]participant.Alias)
	for _, name := range []string{"ada", "ben", "cat", "turing", "grace", "tim", "bob", "later", "linus", "replacement"} {
		aliases[name] = participant.New(name, "builder", participant.InitiativeManual).Alias
	}
	return aliases
})

func testAlias(name string) participant.Alias {
	if alias, ok := aliasFixtures()[name]; ok {
		return alias
	}
	return participant.Alias{Name: name}
}
