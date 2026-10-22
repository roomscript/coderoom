package session

import (
	"fmt"
	"strings"

	"github.com/roomscript/coderoom/internal/participant"
)

// HandoffCommand transfers the latest completed room-visible output from one
// alias to another through a context path and emits a shared-room audit event.
type HandoffCommand struct {
	FromAlias            participant.Alias
	ToAlias              participant.Alias
	RequiredReadyAliases []participant.Alias
	Source               HandoffSource
}

func (c HandoffCommand) execute(s *Session, executionID ExecutionID) (err error) {
	result := createRoutingResult(RoutingHandoff, []participant.Alias{c.ToAlias}, RecipientHandoff)
	defer func() {
		result.record(0, err)
		s.notifyRoutingCompleted(executionID, result, err)
	}()
	attempt := newHandoffAttempt(c, s)
	if err := c.validate(attempt, s); err != nil {
		return err
	}
	if err := c.validateSource(); err != nil {
		return err
	}
	return c.deliver(attempt, s, executionID)
}

type handoffAttempt struct {
	requiredReadyAliases []participant.Alias
	idle                 []participant.Alias
	notReadyAliases      []participant.Alias
	source               HandoffSource
}

func newHandoffAttempt(c HandoffCommand, s *Session) *handoffAttempt {
	requiredReadyAliases, idle, notReadyAliases := handoffReadinessState(c.RequiredReadyAliases, s)
	return &handoffAttempt{
		requiredReadyAliases: requiredReadyAliases,
		idle:                 idle,
		notReadyAliases:      notReadyAliases,
		source:               c.Source,
	}
}

func (c HandoffCommand) validate(attempt *handoffAttempt, s *Session) error {
	if strings.TrimSpace(c.FromAlias.Name) == "" || strings.TrimSpace(c.ToAlias.Name) == "" {
		return fmt.Errorf("usage: /handoff <from> <to>")
	}
	if c.FromAlias == c.ToAlias {
		return fmt.Errorf("handoff requires distinct source and destination aliases")
	}
	for _, alias := range []participant.Alias{c.FromAlias, c.ToAlias} {
		if _, ok := s.Participant(alias); !ok {
			return fmt.Errorf("handoff participant %q: %w", alias.Name, errParticipantNotFound)
		}
	}
	if len(attempt.notReadyAliases) > 0 {
		return fmt.Errorf("handoff requires all participants to be idle: %s", strings.Join(aliasNames(attempt.notReadyAliases), ", "))
	}
	return nil
}

func (c HandoffCommand) validateSource() error {
	if c.Source.Text != "" {
		return nil
	}
	return fmt.Errorf("handoff source %q has no completed room-visible output", c.FromAlias.Name)
}

func (c HandoffCommand) deliver(attempt *handoffAttempt, s *Session, executionID ExecutionID) error {
	a, prepared, err := acquireParticipantForNotice(c.ToAlias, s, executionID)
	if err != nil {
		return err
	}
	payload := formatHandoffPayload(c.FromAlias, attempt.source.Text)
	anchorID, err := a.SendNotice(payload)
	if err != nil {
		s.abortWork(c.ToAlias)
		return fmt.Errorf("handoff to %q: %w", c.ToAlias.Name, err)
	}
	if prepared {
		s.beginParticipantWorking(c.ToAlias, anchorID)
	} else {
		s.trackAnchorStream(c.ToAlias, anchorID)
	}
	notifyHandoffDelivered(c.FromAlias, c.ToAlias, attempt, s)
	return nil
}

func notifyHandoffDelivered(fromAlias, toAlias participant.Alias, attempt *handoffAttempt, s *Session) {
	s.notify(HandoffDelivered{
		FromAlias:            fromAlias,
		ToAlias:              toAlias,
		Text:                 attempt.source.Text,
		Preview:              formatHandoffPreview(fromAlias, toAlias, attempt.source.Text),
		SourceRecordIndex:    attempt.source.RecordIndex,
		RequiredReadyAliases: append([]participant.Alias(nil), attempt.requiredReadyAliases...),
		IdleAliases:          append([]participant.Alias(nil), attempt.idle...),
		NotReadyAliases:      append([]participant.Alias(nil), attempt.notReadyAliases...),
	})
}

func handoffReadinessState(aliases []participant.Alias, s *Session) (requiredReadyAliases []participant.Alias, idle []participant.Alias, notReadyAliases []participant.Alias) {
	if len(aliases) == 0 {
		for _, p := range s.Participants() {
			if !p.IsRoutable() {
				continue
			}
			requiredReadyAliases = append(requiredReadyAliases, p.Alias)
		}
	} else {
		requiredReadyAliases = append([]participant.Alias(nil), aliases...)
		sortAliases(requiredReadyAliases)
	}
	for _, alias := range requiredReadyAliases {
		p, ok := s.Participant(alias)
		if ok && p.IsReadyForWork() {
			idle = append(idle, alias)
			continue
		}
		notReadyAliases = append(notReadyAliases, alias)
	}
	return requiredReadyAliases, idle, notReadyAliases
}

func formatHandoffPayload(fromAlias participant.Alias, text string) string {
	return fmt.Sprintf("[HANDOFF from %s]\n\n%s", fromAlias.Name, text)
}

func formatHandoffPreview(fromAlias, toAlias participant.Alias, text string) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "[handoff %s -> %s]", fromAlias.Name, toAlias.Name)
	sb.WriteString("\n  ↦ source: ")
	sb.WriteString(fromAlias.Name)
	sb.WriteString(" latest output")

	preview, remaining := handoffBodyPreview(text)
	if preview != "" {
		sb.WriteString("\n")
		sb.WriteString(preview)
	}
	if remaining > 0 {
		fmt.Fprintf(&sb, "\n  (+%d more lines; Ctrl+G open transcript)", remaining)
	}
	return sb.String()
}

const handoffPreviewLines = 3
const handoffPreviewMaxCols = 120

func handoffBodyPreview(text string) (string, int) {
	body := strings.TrimRight(text, "\n")
	if body == "" {
		return "", 0
	}
	lines := strings.Split(body, "\n")
	previewCount := min(len(lines), handoffPreviewLines)
	preview := make([]string, 0, previewCount)
	for i := 0; i < previewCount; i++ {
		preview = append(preview, "  > "+truncateHandoffPreviewLine(lines[i], handoffPreviewMaxCols))
	}
	return strings.Join(preview, "\n"), len(lines) - previewCount
}

func truncateHandoffPreviewLine(s string, maxCols int) string {
	runes := []rune(s)
	if maxCols <= 0 || len(runes) <= maxCols {
		return s
	}
	if maxCols == 1 {
		return "…"
	}
	return string(runes[:maxCols-1]) + "…"
}
