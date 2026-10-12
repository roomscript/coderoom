package session_test

import (
	"github.com/roomscript/coderoom/internal/participant"
	"github.com/roomscript/coderoom/internal/session"
)

func resolvedAlias(s *session.Session, name string) participant.Alias {
	if view, ok := s.ParticipantByName(name); ok {
		return view.Alias
	}
	return participant.Alias{Name: name}
}

func resolvedAliases(s *session.Session, names ...string) []participant.Alias {
	aliases := make([]participant.Alias, len(names))
	for i, name := range names {
		aliases[i] = resolvedAlias(s, name)
	}
	return aliases
}

func namesOfAliases(aliases []participant.Alias) []string {
	names := make([]string, len(aliases))
	for i, alias := range aliases {
		names[i] = alias.Name
	}
	return names
}
