package approval

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/roomscript/coderoom/internal/interpreter"
)

func TestUpdate_downSelectsNextOption(t *testing.T) {
	m := New().Set(interpreter.Approval{
		Prompt:  "approve?",
		Options: []interpreter.ApprovalOption{{ID: "decline"}, {ID: "accept"}},
	})
	if got := m.Selected(); got != 0 {
		t.Fatalf("expected selected=0, got %d", got)
	}
	m2, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	if got := m2.Selected(); got != 1 {
		t.Fatalf("expected selected=1 after Down, got %d", got)
	}
}

func TestUpdate_enterEmitsConfirmMsg(t *testing.T) {
	m := New().Set(interpreter.Approval{
		Prompt:  "approve?",
		Options: []interpreter.ApprovalOption{{ID: "decline"}},
	})
	_, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if cmd == nil {
		t.Fatal("expected confirm cmd")
	}
	if _, ok := cmd().(ConfirmMsg); !ok {
		t.Fatalf("expected ConfirmMsg, got %T", cmd())
	}
}

func TestSet_preservesDetachedOptionsAndSelectedID(t *testing.T) {
	options := []interpreter.ApprovalOption{{ID: "custom", Label: "Custom response"}}
	m := New().Set(interpreter.Approval{Prompt: "approve?", Options: options})
	options[0] = interpreter.ApprovalOption{ID: "changed", Label: "Changed"}
	choice, ok := m.SelectedOption()
	if !ok || choice.OptionID != "custom" {
		t.Fatalf("choice = %#v, ok = %v", choice, ok)
	}
	if got := m.View(); got != "approve?\n\n> Custom response" {
		t.Fatalf("view = %q", got)
	}
}

func TestView_boundedOptionsRemainReachable(t *testing.T) {
	for _, size := range []struct{ width, height int }{{8, 1}, {8, 4}, {20, 8}} {
		t.Run(fmt.Sprintf("%dx%d", size.width, size.height), func(t *testing.T) {
			m := New().Set(interpreter.Approval{
				Prompt: strings.Repeat("long request text\n", 20),
				Options: []interpreter.ApprovalOption{
					{ID: "first", Label: "first response with a long label"},
					{ID: "second", Label: "second response with a long label"},
					{ID: "third", Label: "third response with a long label"},
				},
			}).SetSize(size.width, size.height)
			for i := 0; i < 4; i++ {
				view := ansi.Strip(m.View())
				if rows := strings.Count(view, "\n") + 1; rows != m.Height() || rows > size.height {
					t.Fatalf("rendered rows=%d, measured=%d, budget=%d", rows, m.Height(), size.height)
				}
				for _, line := range strings.Split(view, "\n") {
					if ansi.StringWidth(line) > size.width {
						t.Fatalf("line exceeds width: %q", line)
					}
				}
				choice, _ := m.SelectedOption()
				wantLabel := "> " + choice.OptionID
				if size.height == 1 {
					// A one-row panel shares the selected label with overflow markers.
					wantLabel = ansi.Truncate(wantLabel, size.width-3, "")
				}
				if !strings.Contains(view, wantLabel) {
					t.Fatalf("selected option %q is hidden: %q", choice.OptionID, view)
				}
				m, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
			}
		})
	}
}

func TestUpdate_requestPagingAndResizePreserveChoice(t *testing.T) {
	m := New().Set(interpreter.Approval{
		Prompt:  "first\nsecond\nthird\nfourth\nfifth\nlast",
		Options: []interpreter.ApprovalOption{{ID: "accept"}, {ID: "decline"}, {ID: "cancel"}},
	}).SetSize(40, 6)
	m, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	for i := 0; i < 5; i++ {
		m, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyPgDown}))
	}
	if view := m.View(); !strings.Contains(view, "last") || !strings.Contains(view, "> decline") {
		t.Fatalf("paging lost request tail or selected option: %q", view)
	}
	m = m.SetSize(12, 4)
	choice, _ := m.SelectedOption()
	if choice.OptionID != "decline" || !strings.Contains(m.View(), "> decline") {
		t.Fatalf("resize lost selection: %q", m.View())
	}
	m = m.SetSize(40, 6)
	for i := 0; i < 5; i++ {
		m, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyPgUp}))
	}
	if !strings.Contains(m.View(), "first") {
		t.Fatalf("cannot page back to request start: %q", m.View())
	}
	m = m.Set(interpreter.Approval{Prompt: "replacement", Options: []interpreter.ApprovalOption{{ID: "cancel"}}})
	if !strings.Contains(m.View(), "replacement") {
		t.Fatalf("replacement request is not visible: %q", m.View())
	}
}

func TestView_showsCompleteRequestWhenSpaceAllows(t *testing.T) {
	prompt := "[→ eve] approve command execution: python3 -I -c '\nprint(\"Monday\")\nprint(\"Tuesday\")\nprint(\"Wednesday\")\nprint(\"Thursday\")\nprint(\"Friday\")\n' (Print the work days)"
	m := New().Set(interpreter.Approval{
		Prompt:  prompt,
		Options: []interpreter.ApprovalOption{{ID: "accept"}, {ID: "acceptForSession"}, {ID: "decline"}, {ID: "cancel"}},
	}).SetSize(80, 36)
	want := prompt + "\n\n> accept\n  accept for session\n  decline\n  cancel"
	if got := m.View(); got != want {
		t.Fatalf("request should fit without paging:\nwant: %q\n got: %q", want, got)
	}
	if m.Height() != strings.Count(want, "\n")+1 {
		t.Fatalf("panel should use its natural height, got %d", m.Height())
	}
}

func TestView_smallPanelIndicatesHiddenContent(t *testing.T) {
	for _, height := range []int{1, 2, 3, 4} {
		t.Run(fmt.Sprint(height), func(t *testing.T) {
			m := New().Set(interpreter.Approval{
				Prompt:  strings.Repeat("request\n", 10),
				Options: []interpreter.ApprovalOption{{ID: "accept"}, {ID: "decline"}, {ID: "cancel"}},
			}).SetSize(40, height)
			if view := m.View(); !strings.Contains(view, "R↓") || !strings.Contains(view, "O↓") {
				t.Fatalf("hidden request/options need indicators: %q", view)
			}
			m, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyUp}))
			if view := m.View(); !strings.Contains(view, "O↑") {
				t.Fatalf("hidden earlier options need an indicator: %q", view)
			}
		})
	}
}

func TestView_tabsAndWideCharactersFitWidth(t *testing.T) {
	for _, width := range []int{8, 12, 20} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			m := New().Set(interpreter.Approval{
				Prompt:  "界\tcommand\n\tprintf '\\t'\nlast",
				Options: []interpreter.ApprovalOption{{ID: "custom", Label: "界\tresponse"}},
			}).SetSize(width, 40)
			view := m.View()
			if strings.Contains(view, "\t") {
				t.Fatalf("literal tabs can cause unmeasured terminal wrapping: %q", view)
			}
			for _, line := range strings.Split(view, "\n") {
				if ansi.StringWidth(line) > width {
					t.Fatalf("line exceeds width %d: %q", width, line)
				}
			}
			if !strings.Contains(view, "界") || !strings.Contains(view, "last") {
				t.Fatalf("request content was lost: %q", view)
			}
		})
	}
}

func TestView_tabStopsIncludeOptionPrefix(t *testing.T) {
	m := New().Set(interpreter.Approval{
		Prompt:  "\tfirst\n界\tlast",
		Options: []interpreter.ApprovalOption{{ID: "custom", Label: "界\tresponse"}},
	}).SetSize(80, 20)
	want := "        first\n界      last\n\n> 界    response"
	if got := m.View(); got != want {
		t.Fatalf("unexpected tab stops:\nwant: %q\n got: %q", want, got)
	}
}

func TestUpdate_pagingWithoutVisibleRequestIsNoOp(t *testing.T) {
	for _, prompt := range []string{"one line", "first\nsecond\nthird\nfourth"} {
		t.Run(prompt, func(t *testing.T) {
			m := New().Set(interpreter.Approval{
				Prompt:  prompt,
				Options: []interpreter.ApprovalOption{{ID: "accept"}, {ID: "decline"}},
			}).SetSize(40, 2)
			before := m.View()
			if strings.Contains(before, "PgUp/PgDn") || !strings.Contains(before, "R↓") {
				t.Fatalf("hidden request should be indicated without a paging hint: %q", before)
			}
			for _, code := range []rune{tea.KeyPgDown, tea.KeyPgDown, tea.KeyPgUp} {
				m, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: code}))
			}
			if m.requestTop != 0 || m.View() != before {
				t.Fatalf("paging changed a panel with no visible request rows: top=%d view=%q", m.requestTop, m.View())
			}
		})
	}
}
