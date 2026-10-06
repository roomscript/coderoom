package ui

import (
	"os"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/roomscript/coderoom/internal/ui/editor"
	"github.com/roomscript/coderoom/internal/ui/room/history/record"
)

func TestCtrlG_withoutEditorAddsSystemRecordAndPreservesBuffer(t *testing.T) {
	t.Setenv("EDITOR", "")
	t.Setenv("VISUAL", "")

	m := makeReadyModel(t)
	m.room = m.room.SetComposeValue("draft")

	next, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: 'g', Mod: tea.ModCtrl}))
	m2 := next.(Model)
	if got := m2.room.ComposeValue(); got != "draft" {
		t.Fatalf("expected input preserved when editor is unset, got %q", got)
	}
	if !hasRecord(m2, record.KindSystem, "no editor configured") {
		t.Fatalf("expected system record about editor configuration; records: %v", m2.room.HistoryRecords())
	}
}

func TestEditorCompose_cancelRestoresPriorBuffer(t *testing.T) {
	m := makeReadyModel(t)
	m.room = m.room.SetComposeValue("before")

	next, _ := m.Update(editor.Response{
		Purpose:   editor.PurposeCompose,
		PriorText: "before",
		Canceled:  true,
		Err:       os.ErrInvalid,
	})
	m2 := next.(Model)
	if got := m2.room.ComposeValue(); got != "before" {
		t.Fatalf("expected cancel to restore prior buffer, got %q", got)
	}
}

func TestEditorCompose_successReplacesBuffer(t *testing.T) {
	m := makeReadyModel(t)
	m.room = m.room.SetComposeValue("before")

	next, _ := m.Update(editor.Response{
		Purpose:   editor.PurposeCompose,
		PriorText: "before",
		NewText:   "after",
	})
	m2 := next.(Model)
	if got := m2.room.ComposeValue(); got != "after" {
		t.Fatalf("expected success to replace buffer, got %q", got)
	}
}

func TestEditorResponse_transcriptDoesNotMutateComposer(t *testing.T) {
	m := makeReadyModel(t)
	m.room = m.room.SetComposeValue("draft")

	next, _ := m.Update(editor.Response{
		Purpose:  editor.PurposeTranscript,
		NewText:  "ignored",
		Err:      nil,
		Canceled: false,
	})
	m2 := next.(Model)
	if got := m2.room.ComposeValue(); got != "draft" {
		t.Fatalf("expected transcript response to not mutate composer, got %q", got)
	}
}
