package ui

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/trigosec/coderoom/internal/interpreter"
	"github.com/trigosec/coderoom/internal/participant"
)

func (m Model) presentInterpreterStage(snapshot interpreter.Snapshot) Model {
	stage := snapshot.Stage
	if stage == nil {
		return m.clearInterpreterStage()
	}
	if m.interpreterStagePresented && m.preserveStageDraft {
		return m
	}
	m.interpreterStagePresented = true
	m.preserveStageDraft = false
	colorByAlias := snapshotParticipantColor(snapshot.Participants)
	m.room = m.room.SetComposerStaged(stage.Raw, renderInterpreterStageStatus(stage, colorByAlias))
	return m
}

func (m Model) clearInterpreterStage() Model {
	if !m.interpreterStagePresented {
		return m
	}
	m.interpreterStagePresented = false
	m.room = m.room.ClearComposerStaged()
	if !m.preserveStageDraft {
		m.room = m.room.SetComposeValue("")
	}
	m.preserveStageDraft = false
	return m
}

func snapshotParticipantColor(participants []participant.View) func(string) string {
	colors := make(map[string]string, len(participants))
	for _, view := range participants {
		colors[view.Alias] = view.Color
	}
	return func(alias string) string { return colors[alias] }
}

func renderInterpreterStageStatus(
	stage *interpreter.StagedSubmission,
	colorByAlias func(string) string,
) string {
	lead := "Message on-hold."
	tail := "Press Esc to edit."
	if len(stage.Interruptible) > 0 {
		tail += " Press Ctrl+X to interrupt and send."
	}
	if stage.InterruptRequested {
		lead = "Interrupt requested."
		tail = "Waiting to send…"
	}
	busy := "none"
	if len(stage.Blocking) > 0 {
		aliases := make([]string, len(stage.Blocking))
		for index, alias := range stage.Blocking {
			color := colorByAlias(alias)
			if color == "" {
				aliases[index] = alias
				continue
			}
			aliases[index] = lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Render(alias)
		}
		busy = strings.Join(aliases, ", ")
	}
	return lead + " Waiting for: " + busy + ". " + tail
}
