package session

import (
	"fmt"
	"slices"
	"strings"

	"github.com/roomscript/coderoom/internal/participant"
)

// HandoffCommand transfers the latest completed room-visible output from one
// alias to another through a context path and emits a shared-room audit event.
type HandoffCommand struct {
	FromAlias   string
	ToAlias     string
	IdleAliases []string
	Source      HandoffSource
}

func (c HandoffCommand) execute(s *Session) error {
	attempt := newHandoffAttempt(c, s)
	if err := c.validate(attempt, s); err != nil {
		return err
	}
	if err := c.validateSource(attempt, s); err != nil {
		return err
	}
	return c.deliver(attempt, s)
}

type handoffAttempt struct {
	fromAlias string
	toAlias   string
	barrier   []string
	idle      []string
	busy      []string
	source    HandoffSource
}

func newHandoffAttempt(c HandoffCommand, s *Session) *handoffAttempt {
	barrier, idle, busy := handoffBarrierState(c.IdleAliases, s)
	source := c.Source
	if source.Text == "" {
		source.RecordIndex = -1
	}
	return &handoffAttempt{
		fromAlias: c.FromAlias,
		toAlias:   c.ToAlias,
		barrier:   barrier,
		idle:      idle,
		busy:      busy,
		source:    source,
	}
}

func (c HandoffCommand) validate(attempt *handoffAttempt, s *Session) error {
	if strings.TrimSpace(c.FromAlias) == "" || strings.TrimSpace(c.ToAlias) == "" {
		return rejectHandoffAttempt(attempt, s, "usage: /handoff <from> <to>", fmt.Errorf("usage: /handoff <from> <to>"))
	}
	if c.FromAlias == c.ToAlias {
		return rejectHandoffAttempt(attempt, s, "distinct aliases required", fmt.Errorf("handoff requires distinct source and destination aliases"))
	}
	if len(attempt.busy) > 0 {
		return rejectHandoffAttempt(
			attempt,
			s,
			"participants busy",
			fmt.Errorf("handoff requires all participants to be idle: %s", strings.Join(attempt.busy, ", ")),
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
		fmt.Errorf("handoff source %q has no completed room-visible output", c.FromAlias),
	)
}

func (c HandoffCommand) deliver(attempt *handoffAttempt, s *Session) error {
	a, prepared, err := acquireParticipantForNotice(c.ToAlias, s)
	if err != nil {
		return rejectHandoffAttempt(attempt, s, err.Error(), err)
	}
	payload := formatHandoffPayload(c.FromAlias, attempt.source.Text)
	anchorID, err := a.SendNotice(payload)
	if err != nil {
		s.abortWork(c.ToAlias)
		return rejectHandoffAttempt(attempt, s, err.Error(), fmt.Errorf("handoff to %q: %w", c.ToAlias, err))
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
	notifyHandoffRejected(s, attempt.fromAlias, attempt.toAlias, attempt.barrier, attempt.idle, attempt.busy, attempt.source.RecordIndex, reason)
	return err
}

func notifyHandoffDelivered(fromAlias, toAlias string, attempt *handoffAttempt, s *Session) {
	s.notify(ContextHandoff{
		FromAlias:         fromAlias,
		ToAlias:           toAlias,
		Text:              attempt.source.Text,
		Preview:           formatHandoffPreview(fromAlias, toAlias, attempt.source.Text),
		SourceRecordIndex: attempt.source.RecordIndex,
		BarrierAliases:    append([]string(nil), attempt.barrier...),
		IdleAliases:       append([]string(nil), attempt.idle...),
		BusyAliases:       append([]string(nil), attempt.busy...),
	})
	notifyHandoffAccepted(s, fromAlias, toAlias, attempt.barrier, attempt.idle, attempt.source.RecordIndex)
}

func handoffBarrierState(aliases []string, s *Session) (barrier []string, idle []string, busy []string) {
	if len(aliases) == 0 {
		for _, p := range s.Participants() {
			if !p.IsRoutable() {
				continue
			}
			barrier = append(barrier, p.Alias)
		}
	} else {
		barrier = append([]string(nil), aliases...)
		slices.Sort(barrier)
	}
	for _, alias := range barrier {
		p, ok := s.Participant(alias)
		if !ok {
			continue
		}
		if p.Status == participant.StatusIdle {
			idle = append(idle, alias)
			continue
		}
		busy = append(busy, alias)
	}
	return barrier, idle, busy
}

func notifyHandoffAccepted(s *Session, fromAlias, toAlias string, barrier, idle []string, sourceRecordIndex int) {
	s.notify(AgentLog{Text: formatHandoffAttemptLog("accepted", fromAlias, toAlias, barrier, idle, nil, sourceRecordIndex, "")})
}

func notifyHandoffRejected(s *Session, fromAlias, toAlias string, barrier, idle, busy []string, sourceRecordIndex int, reason string) {
	reason = compactHandoffReason(reason)
	s.notify(AgentLog{Text: formatHandoffAttemptLog("rejected", fromAlias, toAlias, barrier, idle, busy, sourceRecordIndex, reason)})
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

func formatHandoffAttemptLog(status, fromAlias, toAlias string, barrier, idle, busy []string, sourceRecordIndex int, reason string) string {
	var sb strings.Builder
	sb.WriteString("handoff ")
	sb.WriteString(status)
	sb.WriteString(": from=")
	sb.WriteString(fromAlias)
	sb.WriteString(" to=")
	sb.WriteString(toAlias)
	sb.WriteString(" barrier=")
	sb.WriteString(formatHandoffAliasList(barrier))
	sb.WriteString(" idle=")
	sb.WriteString(formatHandoffAliasList(idle))
	if len(busy) > 0 {
		sb.WriteString(" busy=")
		sb.WriteString(formatHandoffAliasList(busy))
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

func formatHandoffAliasList(aliases []string) string {
	if len(aliases) == 0 {
		return "[]"
	}
	return "[" + strings.Join(aliases, ",") + "]"
}

func formatHandoffPayload(fromAlias string, text string) string {
	return fmt.Sprintf("[HANDOFF from %s]\n\n%s", fromAlias, text)
}

func formatHandoffPreview(fromAlias, toAlias, text string) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "[handoff %s -> %s]", fromAlias, toAlias)
	sb.WriteString("\n  ↦ source: ")
	sb.WriteString(fromAlias)
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
