package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/roomscript/coderoom/internal/ui/room/history/record"
)

func TestWhoEcho_twiceRendersTwoEchosInTallTerminal(t *testing.T) {
	// Reproduce the interactive path (KeyRunes + Enter) rather than calling
	// handleEnter directly.
	// Reserve one row for the UI header.
	m := makeReadyModelWithHeight(t, 41)

	m = submitWhoInteractive(t, m)
	m = submitWhoInteractive(t, m)

	// Ensure the underlying content contains both echos regardless of scroll.
	content := ansi.Strip(m.room.HistoryRenderedContent())
	if strings.Count(content, "❯ /who") != 2 {
		t.Fatalf("expected rendered history to contain two echos, got:\n%s", content)
	}

	userInputs := 0
	for _, r := range m.room.HistoryRecords() {
		if r.Kind == record.KindUserInput && strings.TrimSpace(r.Text) == "/who" {
			userInputs++
		}
	}
	if userInputs != 2 {
		t.Fatalf("expected two echoed user input records, got %d; records=%v", userInputs, m.room.HistoryRecords())
	}

	// The viewport should stay at the top when all content fits.
	contentLines := strings.Count(ansi.Strip(m.room.HistoryRenderedContent()), "\n") + 1
	if contentLines <= m.room.HistoryHeight() && m.room.YOffset() != 0 {
		t.Fatalf("expected YOffset=0 when content fits (contentLines=%d height=%d), got %d", contentLines, m.room.HistoryHeight(), m.room.YOffset())
	}
	view := ansi.Strip(m.room.HistoryView())
	if strings.Count(view, "❯ /who") != 2 {
		t.Fatalf("expected two visible echos without scrolling; got:\n%s", view)
	}
}

func TestWhoEcho_twiceVisibleInSmallTerminal(t *testing.T) {
	// Regression guard for the "missing first line" symptom in small terminals:
	// `/who` twice should fit without scrolling and show both echos/results.
	// Reserve one row for the UI header.
	m := makeReadyModelWithHeight(t, 11)

	m = submitWhoInteractive(t, m)
	m = submitWhoInteractive(t, m)

	contentLines := strings.Count(ansi.Strip(m.room.HistoryRenderedContent()), "\n") + 1
	if contentLines <= m.room.HistoryHeight() && m.room.YOffset() != 0 {
		t.Fatalf("expected YOffset=0 when content fits (contentLines=%d height=%d), got %d", contentLines, m.room.HistoryHeight(), m.room.YOffset())
	}
	view := ansi.Strip(m.room.HistoryView())
	// When content fits, both /who invocations and results should be visible.
	if contentLines <= m.room.HistoryHeight() {
		if strings.Count(view, "❯ /who") != 2 {
			t.Fatalf("expected two visible echos; got:\n%s", view)
		}
		if strings.Count(view, "[no agents]") != 2 {
			t.Fatalf("expected two visible /who results; got:\n%s", view)
		}
	}
}

func submitWhoInteractive(t *testing.T, m Model) Model {
	t.Helper()
	// Paste exercises the multi-rune input path used by terminals and IMEs.
	next, _ := m.Update(tea.PasteMsg{Content: "/who"})
	m = next.(Model)
	next, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	m = next.(Model)
	if cmd == nil {
		return m
	}
	next, _ = m.Update(cmd())
	m = next.(Model)
	if !m.submissionPending {
		return m
	}
	return processInterpreterSubmission(t, m)
}
