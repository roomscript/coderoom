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
	if err := c.validateSource(attempt, s); err != nil {
		return err
	}
	return c.deliver(attempt, s, executionID)
}

type handoffAttempt struct {
	fromAlias            participant.Alias
	toAlias              participant.Alias
	requiredReadyAliases []participant.Alias
	idle                 []participant.Alias
	notReadyAliases      []participant.Alias
	source               HandoffSource
}

func newHandoffAttempt(c HandoffCommand, s *Session) *handoffAttempt {
	requiredReadyAliases, idle, notReadyAliases := handoffReadinessState(c.RequiredReadyAliases, s)
	source := c.Source
	if source.Text == "" {
		source.RecordIndex = -1
	}
	return &handoffAttempt{
		fromAlias:            c.FromAlias,
		toAlias:              c.ToAlias,
		requiredReadyAliases: requiredReadyAliases,
		idle:                 idle,
		notReadyAliases:      notReadyAliases,
		source:               source,
	}
}

func (c HandoffCommand) validate(attempt *handoffAttempt, s *Session) error {
	if strings.TrimSpace(c.FromAlias.Name) == "" || strings.TrimSpace(c.ToAlias.Name) == "" {
		return rejectHandoffAttempt(attempt, s, "usage: /handoff <from> <to>", fmt.Errorf("usage: /handoff <from> <to>"))
	}
	if c.FromAlias == c.ToAlias {
		return rejectHandoffAttempt(attempt, s, "distinct aliases required", fmt.Errorf("handoff requires distinct source and destination aliases"))
	}
	for _, alias := range []participant.Alias{c.FromAlias, c.ToAlias} {
		if _, ok := s.Participant(alias); !ok {
			return rejectHandoffAttempt(attempt, s, "participant departed or replaced", fmt.Errorf("handoff participant %q: %w", alias.Name, errParticipantNotFound))
		}
	}
	if len(attempt.notReadyAliases) > 0 {
		return rejectHandoffAttempt(
			attempt,
			s,
			"participants busy",
			fmt.Errorf("handoff requires all participants to be idle: %s", strings.Join(aliasNames(attempt.notReadyAliases), ", ")),
		)
	}
	return nil
}

func (c HandoffCommand) validateSource(attempt *handoffAttempt, s *Session) error {
	if c.Source.Text != "" {
		return nil
	}
	return rejectHandoffAttempt(
		attempt,
		s,
		"no completed room-visible output",
		fmt.Errorf("handoff source %q has no completed room-visible output", c.FromAlias.Name),
	)
}

func (c HandoffCommand) deliver(attempt *handoffAttempt, s *Session, executionID ExecutionID) error {
	a, prepared, err := acquireParticipantForNotice(c.ToAlias, s, executionID)
	if err != nil {
		return rejectHandoffAttempt(attempt, s, err.Error(), err)
	}
	payload := formatHandoffPayload(c.FromAlias, attempt.source.Text)
	anchorID, err := a.SendNotice(payload)
	if err != nil {
		s.abortWork(c.ToAlias)
		return rejectHandoffAttempt(attempt, s, err.Error(), fmt.Errorf("handoff to %q: %w", c.ToAlias.Name, err))
	}
	if prepared {
		s.beginParticipantWorking(c.ToAlias, anchorID)
	} else {
		s.trackAnchorStream(c.ToAlias, anchorID)
	}
	notifyHandoffDelivered(c.FromAlias, c.ToAlias, attempt, s)
	return nil
}

func rejectHandoffAttempt(attempt *handoffAttempt, s *Session, reason string, err error) error {
	notifyHandoffRejected(s, attempt.fromAlias, attempt.toAlias, attempt.requiredReadyAliases, attempt.idle, attempt.notReadyAliases, attempt.source.RecordIndex, reason)
	return err
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
	notifyHandoffAccepted(s, fromAlias, toAlias, attempt.requiredReadyAliases, attempt.idle, attempt.source.RecordIndex)
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

func notifyHandoffAccepted(s *Session, fromAlias, toAlias participant.Alias, requiredReadyAliases, idle []participant.Alias, sourceRecordIndex int) {
	s.notify(AgentLog{Text: formatHandoffAttemptLog("accepted", fromAlias, toAlias, requiredReadyAliases, idle, nil, sourceRecordIndex, "")})
}

func notifyHandoffRejected(s *Session, fromAlias, toAlias participant.Alias, requiredReadyAliases, idle, notReadyAliases []participant.Alias, sourceRecordIndex int, reason string) {
	reason = compactHandoffReason(reason)
	s.notify(AgentLog{Text: formatHandoffAttemptLog("rejected", fromAlias, toAlias, requiredReadyAliases, idle, notReadyAliases, sourceRecordIndex, reason)})
}

func compactHandoffReason(reason string) string {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return ""
	}
	lines := strings.FieldsFunc(reason, func(r rune) bool {
		return r == '\n' || r == '\r'
	})
	parts := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts = append(parts, line)
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " | ")
}

func formatHandoffAttemptLog(status string, fromAlias, toAlias participant.Alias, requiredReadyAliases, idle, notReadyAliases []participant.Alias, sourceRecordIndex int, reason string) string {
	var sb strings.Builder
	sb.WriteString("handoff ")
	sb.WriteString(status)
	sb.WriteString(": from=")
	sb.WriteString(fromAlias.Name)
	sb.WriteString(" to=")
	sb.WriteString(toAlias.Name)
	sb.WriteString(" barrier=")
	sb.WriteString(formatHandoffAliasList(requiredReadyAliases))
	sb.WriteString(" idle=")
	sb.WriteString(formatHandoffAliasList(idle))
	if len(notReadyAliases) > 0 {
		sb.WriteString(" busy=")
		sb.WriteString(formatHandoffAliasList(notReadyAliases))
	}
	sb.WriteString(" source_record=")
	if sourceRecordIndex < 0 {
		sb.WriteString("none")
	} else {
		fmt.Fprintf(&sb, "%d", sourceRecordIndex)
	}
	if strings.TrimSpace(reason) != "" {
		sb.WriteString(" reason=")
		sb.WriteString(reason)
	}
	return sb.String()
}

func formatHandoffAliasList(aliases []participant.Alias) string {
	if len(aliases) == 0 {
		return "[]"
	}
	return "[" + strings.Join(aliasNames(aliases), ",") + "]"
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
