package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/roomscript/coderoom/internal/interpreter"
)

func TestView_doesNotEndWithNewline(t *testing.T) {
	m := makeReadyModelWithHeight(t, 48)

	// Add some content so we exercise the viewport/input/toolbox composition.
	m.room = m.room.AppendSystem("[x]")

	view := m.View().Content
	if strings.HasSuffix(view, "\n") {
		// Bug/regression guard:
		// When the rendered frame height matches the terminal height, a trailing
		// newline can scroll the terminal by 1 row. In practice this made the
		// first viewport line appear "missing" (e.g. after `/who` twice, the
		// screen started at the second record) even though viewport.YOffset=0 and
		// viewport.View() contained the correct top-of-history line.
		t.Fatalf("expected View() not to end with newline; trailing newline can scroll terminal and hide the first row")
	}
}

func TestView_doesNotExceedTerminalHeight(t *testing.T) {
	const height = 30
	m := makeReadyModelWithHeight(t, height)

	// Add a few records so the viewport isn't empty.
	for i := 0; i < 3; i++ {
		m.room = m.room.AppendSystem("[x]")
	}

	view := m.View().Content
	lines := 0
	if view != "" {
		lines = strings.Count(view, "\n") + 1
	}
	if lines > height {
		t.Fatalf("expected View() to render at most terminal height=%d lines, got %d", height, lines)
	}
}

func TestView_longApprovalFitsTerminal(t *testing.T) {
	const height = 20
	m := makeReadyModelWithHeight(t, height)
	m.room = m.room.ShowApproval(interpreter.Approval{
		Prompt:  strings.Repeat("approval command line\n", 40),
		Options: []interpreter.ApprovalOption{{ID: "accept"}, {ID: "decline"}, {ID: "cancel"}},
	})
	view := ansi.Strip(m.View().Content)
	if rows := strings.Count(view, "\n") + 1; rows > height {
		t.Fatalf("approval frame has %d rows, terminal height %d", rows, height)
	}
	for _, label := range []string{"> accept", "  decline", "  cancel"} {
		if !strings.Contains(view, label) {
			t.Fatalf("approval option %q is missing from frame:\n%s", label, view)
		}
	}
}
