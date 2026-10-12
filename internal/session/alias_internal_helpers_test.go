package session

import "github.com/roomscript/coderoom/internal/participant"

func resolvedAda(s *Session) participant.Alias {
	if view, ok := s.ParticipantByName("ada"); ok {
		return view.Alias
	}
	return participant.Alias{Name: "ada"}
}
