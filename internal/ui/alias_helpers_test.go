package ui

import (
	"sync"
	"testing"

	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/session"
)

var fixtureAliases = sync.OnceValue(func() map[string]participant.Alias {
	aliases := make(map[string]participant.Alias)
	for _, name := range []string{"ada", "bob", "turing", "tim", "cat"} {
		aliases[name] = participant.New(name, "", participant.InitiativeManual).Alias
	}
	return aliases
})

func testAlias(name string) participant.Alias {
	if alias, ok := fixtureAliases()[name]; ok {
		return alias
	}
	return participant.Alias{Name: name}
}

func sessionAlias(t *testing.T, sess *session.Session, name string) participant.Alias {
	t.Helper()
	view, ok := sess.ParticipantByName(name)
	if !ok {
		t.Fatalf("missing participant %s", name)
	}
	return view.Alias
}
