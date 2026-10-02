package ui

import (
	"strings"
	"testing"

	"github.com/trigosec/coderoom/internal/agent"
	"github.com/trigosec/coderoom/internal/interpreter"
	"github.com/trigosec/coderoom/internal/participant"
	"github.com/trigosec/coderoom/internal/session"
)

func TestInterpreterStagePresenterShowsRefreshesAndClearsStage(t *testing.T) {
	m := makeReadyModel(t)
	recordsBefore := m.room.HistoryRecords()

	m = presentStageSnapshot(m, &interpreter.StagedSubmission{
		Raw: "@ada hello", Blocking: []string{"ada"},
	})
	if got := m.room.ComposeValue(); got != "@ada hello" || !m.room.IsComposerStaged() {
		t.Fatalf("composer = %q, staged = %v", got, m.room.IsComposerStaged())
	}
	if view := m.room.View(); !strings.Contains(view, "Participants busy: ada.") {
		t.Fatalf("stage status missing busy participant:\n%s", view)
	}

	m = presentStageSnapshot(m, &interpreter.StagedSubmission{
		Raw: "@ada hello", InterruptRequested: true,
	})
	if view := m.room.View(); !strings.Contains(view, "Interrupt requested.") ||
		!strings.Contains(view, "Waiting to send…") {
		t.Fatalf("stage status was not refreshed:\n%s", view)
	}

	m = presentStageSnapshot(m, nil)
	if m.room.IsComposerStaged() || m.room.ComposeValue() != "" {
		t.Fatalf("cleared composer = %q, staged = %v", m.room.ComposeValue(), m.room.IsComposerStaged())
	}
	if got := m.room.HistoryRecords(); len(got) != len(recordsBefore) {
		t.Fatalf("stage presentation changed transcript: before=%d after=%d", len(recordsBefore), len(got))
	}
}

func TestInterpreterStagePresenterPreservesApprovalOverlay(t *testing.T) {
	tests := []struct {
		name          string
		clearAtStage  bool
		approvalFirst bool
	}{
		{name: "approval then stage", approvalFirst: true},
		{name: "stage then approval"},
		{name: "stage clears behind approval", approvalFirst: true, clearAtStage: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := makeReadyModel(t)
			stage := &interpreter.StagedSubmission{Raw: "hello", Blocking: []string{"ada"}}
			if tt.approvalFirst {
				m = showTestApproval(m)
				m = presentStageSnapshot(m, stage)
			} else {
				m = presentStageSnapshot(m, stage)
				m = showTestApproval(m)
			}
			if tt.clearAtStage {
				m = presentStageSnapshot(m, nil)
			}
			if view := m.room.View(); !strings.Contains(view, "approve?") {
				t.Fatalf("stage snapshot replaced approval overlay:\n%s", view)
			}

			m = pushEvent(m, session.ApprovalCleared{ID: 7})
			if got := m.room.IsComposerStaged(); got == tt.clearAtStage {
				t.Fatalf("staged after approval clear = %v, want %v", got, !tt.clearAtStage)
			}
		})
	}
}

func TestInterpreterStagePresenterRestoresEditedDraftAcrossEventOrder(t *testing.T) {
	tests := []struct {
		name          string
		snapshotFirst bool
	}{
		{name: "result before snapshot"},
		{name: "snapshot before result", snapshotFirst: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := makeReadyModel(t)
			m = presentStageSnapshot(m, &interpreter.StagedSubmission{Raw: "@ada hello"})
			if tt.snapshotFirst {
				m = presentStageSnapshot(m, nil)
			}
			m = m.handleStageTakenForEdit(stageTakenForEditMsg{raw: "@ada hello", ok: true})
			m.room = m.room.SetComposeValue("@ada hello!")
			if !tt.snapshotFirst {
				m = presentStageSnapshot(m, nil)
			}
			if got := m.room.ComposeValue(); got != "@ada hello!" {
				t.Fatalf("composer = %q, want edited draft", got)
			}
			if m.room.IsComposerStaged() {
				t.Fatal("composer remained staged")
			}
		})
	}
}

func TestInterpreterStagePresenterIgnoresStaleStageAfterTakeForEdit(t *testing.T) {
	m := makeReadyModel(t)
	stage := &interpreter.StagedSubmission{Raw: "@ada hello"}
	m = presentStageSnapshot(m, stage)
	m = m.handleStageTakenForEdit(stageTakenForEditMsg{raw: stage.Raw, ok: true})
	m.room = m.room.SetComposeValue("@ada hello!")

	// This non-nil snapshot was queued before TakeStageForEdit removed the stage.
	m = presentStageSnapshot(m, stage)
	m = presentStageSnapshot(m, nil)

	if got := m.room.ComposeValue(); got != "@ada hello!" {
		t.Fatalf("composer = %q, want edited draft", got)
	}
	if m.room.IsComposerStaged() {
		t.Fatal("stale snapshot restored staged mode")
	}
}

func TestInterpreterStagePresenterUsesSnapshotParticipantColors(t *testing.T) {
	stage := &interpreter.StagedSubmission{Raw: "hello", Blocking: []string{"ada"}}
	snapshot := interpreter.Snapshot{
		Stage:        stage,
		Participants: []participant.View{{Alias: "ada", Color: "#ff0000"}},
	}

	status := renderInterpreterStageStatus(stage, snapshotParticipantColor(snapshot.Participants))
	if !strings.Contains(status, "\x1b[") {
		t.Fatalf("status does not contain snapshot participant color: %q", status)
	}
}

func presentStageSnapshot(m Model, stage *interpreter.StagedSubmission) Model {
	next, _ := m.Update(interpreterEventMsg{event: interpreter.StateChanged{
		Snapshot: interpreter.Snapshot{Stage: stage},
	}})
	return next.(Model)
}

func showTestApproval(m Model) Model {
	return pushEvent(m, session.ApprovalRequested{
		Alias: "ada", ID: 7,
		Req: agent.ApprovalRequest{
			Ask: "approve?", Options: []agent.ApprovalOption{agent.OptionAccept},
		},
	})
}

func TestRenderInterpreterStageStatus_includesModeAndBusySummary(t *testing.T) {
	tests := []struct {
		name   string
		stage  interpreter.StagedSubmission
		want   []string
		absent []string
	}{
		{name: "on hold with none busy", want: []string{"Message on-hold.", "Participants busy: none.", "Press Esc to edit.", "Press Ctrl+X to interrupt and send."}, absent: []string{"Interrupt requested.", "Waiting to send…"}},
		{name: "interrupt waiting", stage: interpreter.StagedSubmission{InterruptRequested: true, Blocking: []string{"ada"}}, want: []string{"Interrupt requested.", "Participants busy: ada.", "Waiting to send…"}, absent: []string{"Press Esc to edit."}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := renderInterpreterStageStatus(&tt.stage, func(string) string { return "" })
			for _, text := range tt.want {
				if !strings.Contains(got, text) {
					t.Fatalf("status = %q, missing %q", got, text)
				}
			}
			for _, text := range tt.absent {
				if strings.Contains(got, text) {
					t.Fatalf("status = %q, unexpectedly contains %q", got, text)
				}
			}
		})
	}
}
