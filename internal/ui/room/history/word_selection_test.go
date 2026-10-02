package history

import "testing"

func TestWordSelectionBoundaries(t *testing.T) {
	tests := []struct {
		name             string
		lines            []string
		row, col         int
		moves            string
		wantRow, wantCol int
		want             string
	}{
		{"first word", []string{"one two"}, 0, 0, "R", 0, 3, "one"},
		{"last word before paragraph break", []string{"one two", "", "three"}, 0, 4, "R", 0, 7, "two"},
		{"last word at surface end", []string{"one two"}, 0, 4, "R", 0, 7, "two"},
		{"inside word", []string{"one two"}, 0, 1, "R", 0, 3, "ne"},
		{"skip spaces", []string{"one   two"}, 0, 3, "R", 0, 9, "   two"},
		{"skip paragraph break", []string{"one", "", "two"}, 0, 3, "R", 2, 3, "\n\ntwo"},
		{"wide text", []string{"界面 next"}, 0, 0, "R", 0, 4, "界面"},
		{"punctuation", []string{"hello, next"}, 0, 0, "R", 0, 6, "hello,"},
		{"expand forward", []string{"one two three"}, 0, 0, "RR", 0, 7, "one two"},
		{"shrink forward", []string{"one two three"}, 0, 0, "RRL", 0, 3, "one"},
		{"collapse forward", []string{"one two"}, 0, 0, "RL", 0, 0, ""},
		{"shrink backward", []string{"one two three"}, 0, 13, "LLR", 0, 8, "three"},
		{"collapse backward", []string{"one two"}, 0, 7, "LR", 0, 7, ""},
		{"backward last word", []string{"one two", "", "three"}, 0, 7, "L", 0, 4, "two"},
		{"shrink across lines", []string{"one", "two"}, 0, 0, "RRL", 0, 3, "one"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := New(nil, "")
			for _, line := range tt.lines {
				m.lines = append(m.lines, historyLine{raw: line, plain: line})
			}
			m.cursor = Cursor{Row: tt.row, Col: tt.col, Visible: true}
			for _, move := range tt.moves {
				if move == 'R' {
					m = m.SelectWordRight()
				} else {
					m = m.SelectWordLeft()
				}
			}
			if m.cursor.Row != tt.wantRow || m.cursor.Col != tt.wantCol || m.cursor.PreferredCol != tt.wantCol {
				t.Fatalf("cursor = %+v, want row %d, column %d", m.cursor, tt.wantRow, tt.wantCol)
			}
			got, ok := m.SelectedText()
			if got != tt.want || ok != (tt.want != "") {
				t.Fatalf("selection = (%q, %v), want %q", got, ok, tt.want)
			}
		})
	}
}
