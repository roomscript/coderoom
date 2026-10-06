package history

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/roomscript/coderoom/internal/agent"
	roomstate "github.com/roomscript/coderoom/internal/room"
	rec "github.com/roomscript/coderoom/internal/ui/room/history/record"
)

func TestRenderCursorLine_preservesStyledContent(t *testing.T) {
	styled := lipgloss.NewStyle().Foreground(lipgloss.Color("2")).Render("hello")

	got := renderCursorLine(styled, 1, 10)

	if !strings.Contains(got, "\x1b[32m") {
		t.Fatalf("expected cursor line to preserve existing color styling, got %q", got)
	}
	if !strings.Contains(got, "\x1b[7m") {
		t.Fatalf("expected cursor line to add reverse-video cursor, got %q", got)
	}
}

func TestRenderCursorLine_usesDisplayWidthForWideGraphemes(t *testing.T) {
	got := renderCursorLine("a界b", 1, 10)

	if !strings.Contains(got, "a\x1b[7m界\x1b[27mb") {
		t.Fatalf("expected cursor to highlight the wide grapheme at display column 1, got %q", got)
	}
}

func TestRenderCursorLine_endOfFullWidthLineDoesNotOverflow(t *testing.T) {
	got := renderCursorLine("hello", 5, 5)

	if strings.Count(got, "\n") != 0 {
		t.Fatalf("expected full-width EOL cursor rendering to stay on one line, got %q", got)
	}
	if ansi.StringWidth(got) != 5 {
		t.Fatalf("expected full-width EOL cursor rendering width=5, got %d in %q", ansi.StringWidth(got), got)
	}
	if !strings.Contains(got, "\x1b[7m") {
		t.Fatalf("expected EOL cursor rendering to stay visible, got %q", got)
	}
}

func TestRenderSelectionLine_highlightsSelectedRange(t *testing.T) {
	got := renderSelectionLine("hello", 1, 4, true, 0, false, 10)

	if strings.Count(got, "\x1b[48;5;238m") != 3 {
		t.Fatalf("expected selected range to be highlighted, got %q", got)
	}
}

func TestRenderSelectionLine_combinesCursorAndSelection(t *testing.T) {
	got := renderSelectionLine("hello", 1, 4, true, 2, true, 10)

	if !strings.Contains(got, "\x1b[48;5;238m") {
		t.Fatalf("expected selected range background, got %q", got)
	}
	if !strings.Contains(got, "\x1b[7m") {
		t.Fatalf("expected cursor reverse-video inside selection, got %q", got)
	}
}

func TestRenderSelectionLine_preservesSelectionWhenCursorAtLineEnd(t *testing.T) {
	got := renderSelectionLine("hello", 2, 5, true, 5, true, 10)

	if strings.Count(got, "\x1b[48;5;238m") != 3 {
		t.Fatalf("expected selected tail to remain highlighted at EOL, got %q", got)
	}
	if !strings.Contains(got, "\x1b[7m") {
		t.Fatalf("expected EOL cursor to remain visible, got %q", got)
	}
}

func TestSelectionColumnsForRow_wrapsAcrossRows(t *testing.T) {
	m := New(nil, "")
	m = m.SetSize(4, 4)
	m = m.ReplaceSnapshot(roomstate.Snapshot{
		Records: []rec.Record{{Kind: rec.KindSystem, Text: "abcdef"}},
	})
	m.cursor = Cursor{Row: 0, Col: 2, PreferredCol: 2, Visible: true}
	m.selection = Selection{
		Anchor:  Cursor{Row: 1, Col: 1, PreferredCol: 1, Visible: true},
		Visible: true,
	}

	start0, end0, ok0 := m.selectionColumnsForRow(0)
	if !ok0 || start0 != 2 || end0 != 4 {
		t.Fatalf("row0 selection = (%d,%d,%v), want (2,4,true)", start0, end0, ok0)
	}
	start1, end1, ok1 := m.selectionColumnsForRow(1)
	if !ok1 || start1 != 0 || end1 != 2 {
		t.Fatalf("row1 selection = (%d,%d,%v), want (0,2,true)", start1, end1, ok1)
	}
}

func TestSelectedText_returnsVisibleSelectionAsPlainText(t *testing.T) {
	m := New(nil, "")
	m = m.SetSize(4, 4)
	m = m.ReplaceSnapshot(roomstate.Snapshot{
		Records: []rec.Record{{Kind: rec.KindSystem, Text: "abcdef"}},
	})
	m.cursor = Cursor{Row: 0, Col: 2, PreferredCol: 2, Visible: true}
	m.selection = Selection{
		Anchor:  Cursor{Row: 1, Col: 1, PreferredCol: 1, Visible: true},
		Visible: true,
	}

	got, ok := m.SelectedText()
	if !ok {
		t.Fatal("expected selected text")
	}
	if got != "cd\nef" {
		t.Fatalf("selected text = %q, want %q", got, "cd\nef")
	}
}

func TestSelectedText_collapsedSelectionIsInactive(t *testing.T) {
	m := New(nil, "")
	m = m.SetSize(10, 4)
	m = m.ReplaceSnapshot(roomstate.Snapshot{
		Records: []rec.Record{{Kind: rec.KindSystem, Text: "hello"}},
	})
	m.cursor = Cursor{Row: 0, Col: 2, PreferredCol: 2, Visible: true}
	m.selection = Selection{
		Anchor:  Cursor{Row: 0, Col: 2, PreferredCol: 2, Visible: true},
		Visible: true,
	}

	got, ok := m.SelectedText()
	if ok || got != "" {
		t.Fatalf("collapsed selection = (%q,%v), want empty inactive selection", got, ok)
	}
}

func TestSelectedText_includesCursorCellWhenSelectionExtendsRight(t *testing.T) {
	m := New(nil, "")
	m = m.SetSize(10, 4)
	m = m.ReplaceSnapshot(roomstate.Snapshot{
		Records: []rec.Record{{Kind: rec.KindSystem, Text: "hello"}},
	})
	m.cursor = Cursor{Row: 0, Col: 3, PreferredCol: 3, Visible: true}
	m.selection = Selection{
		Anchor:  Cursor{Row: 0, Col: 1, PreferredCol: 1, Visible: true},
		Visible: true,
	}

	got, ok := m.SelectedText()
	if !ok {
		t.Fatal("expected selected text")
	}
	if got != "ell" {
		t.Fatalf("selected text = %q, want %q", got, "ell")
	}
}

func TestSelectedText_isSymmetricAcrossSelectionDirections(t *testing.T) {
	m := New(nil, "")
	m = m.SetSize(10, 4)
	m = m.ReplaceSnapshot(roomstate.Snapshot{
		Records: []rec.Record{{Kind: rec.KindSystem, Text: "hello"}},
	})

	m.selection = Selection{Anchor: Cursor{Row: 0, Col: 1, Visible: true}, Visible: true}
	m.cursor = Cursor{Row: 0, Col: 3, Visible: true}
	forward, ok := m.SelectedText()
	if !ok {
		t.Fatal("expected forward selection")
	}

	m.selection.Anchor = Cursor{Row: 0, Col: 3, Visible: true}
	m.cursor = Cursor{Row: 0, Col: 1, Visible: true}
	backward, ok := m.SelectedText()
	if !ok {
		t.Fatal("expected backward selection")
	}
	if forward != "ell" || backward != forward {
		t.Fatalf("selected text forward=%q backward=%q, want both %q", forward, backward, "ell")
	}
}

func TestSelectedText_removesLayoutIndentAndPreservesContentIndent(t *testing.T) {
	m := New(nil, "")
	m = m.SetSize(20, 6)
	record := roomstate.NewAgentRecord("ada", agent.Message{
		Content: agent.Output{Text: "one\n  code"},
	})
	m = m.ReplaceSnapshot(roomstate.Snapshot{Records: []rec.Record{record}})
	m.cursor = Cursor{Row: 2, Col: 0, Visible: true}
	m.selection = Selection{
		Anchor:  Cursor{Row: 3, Col: lineWidth(m.lines[3]), Visible: true},
		Visible: true,
	}

	got, ok := m.SelectedText()
	if !ok {
		t.Fatal("expected selected agent body")
	}
	if got != "one\n  code" {
		t.Fatalf("selected text = %q, want %q", got, "one\n  code")
	}
}

func TestSelectedText_removesToolRecordLayoutIndent(t *testing.T) {
	exitCode := 0
	tests := []struct {
		name       string
		record     rec.Record
		wantPrefix string
		wantText   string
	}{
		{
			name: "command",
			record: roomstate.NewAgentRecord("ada", agent.Message{Content: agent.Command{
				Command: "echo hi", Output: "  indented\n", ExitCode: &exitCode,
			}}),
			wantPrefix: "$ echo hi",
			wantText:   "\n  indented\n",
		},
		{
			name: "file change",
			record: roomstate.NewAgentRecord("ada", agent.Message{Content: agent.FileChangeSet{
				Status:  agent.ToolStatusCompleted,
				Changes: []agent.FileChange{{Path: "a.txt", ChangeKind: "update", Diff: "line"}},
			}}),
			wantPrefix: "✎ files:",
			wantText:   "\n- update a.txt\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := New(nil, "")
			m = m.SetSize(40, 12)
			m = m.ReplaceSnapshot(roomstate.Snapshot{Records: []rec.Record{tt.record}})
			m.cursor = Cursor{Row: 2, Col: 0, Visible: true}
			lastRow := len(m.lines) - 1
			m.selection = Selection{
				Anchor:  Cursor{Row: lastRow, Col: lineWidth(m.lines[lastRow]), Visible: true},
				Visible: true,
			}

			got, ok := m.SelectedText()
			if !ok {
				t.Fatal("expected selected tool record body")
			}
			if !strings.HasPrefix(got, tt.wantPrefix) {
				t.Fatalf("selected text = %q, want prefix %q", got, tt.wantPrefix)
			}
			if !strings.Contains(got, tt.wantText) {
				t.Fatalf("selected text = %q, want content %q", got, tt.wantText)
			}
		})
	}
}

func TestSelectedText_removesTrailingWhitespace(t *testing.T) {
	m := New(nil, "")
	m = m.SetSize(10, 4)
	m = m.ReplaceSnapshot(roomstate.Snapshot{
		Records: []rec.Record{{Kind: rec.KindSystem, Text: "text  \n  indented\t "}},
	})
	m.cursor = Cursor{Row: 0, Col: 0, Visible: true}
	m.selection = Selection{
		Anchor:  Cursor{Row: 1, Col: lineWidth(m.lines[1]), Visible: true},
		Visible: true,
	}

	got, ok := m.SelectedText()
	if !ok || got != "text\n  indented" {
		t.Fatalf("selected text = (%q,%v), want (%q,true)", got, ok, "text\n  indented")
	}
}

func TestSelectedText_excludesViewportTrailingPadding(t *testing.T) {
	m := New(nil, "")
	m = m.SetSize(10, 4)
	m = m.ReplaceSnapshot(roomstate.Snapshot{
		Records: []rec.Record{{Kind: rec.KindSystem, Text: "text"}},
	})
	m.cursor = Cursor{Row: 0, Col: 0, Visible: true}
	m.selection = Selection{
		Anchor:  Cursor{Row: 0, Col: lineWidth(m.lines[0]), Visible: true},
		Visible: true,
	}

	if paddedWidth := ansi.StringWidth(m.renderViewportRow(0, true)); paddedWidth != 10 {
		t.Fatalf("rendered row width = %d, want viewport width 10", paddedWidth)
	}
	got, ok := m.SelectedText()
	if !ok || got != "text" {
		t.Fatalf("selected text = (%q,%v), want (%q,true)", got, ok, "text")
	}
}
