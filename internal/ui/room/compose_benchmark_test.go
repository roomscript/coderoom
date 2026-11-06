package room

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func BenchmarkComposeKeystroke(b *testing.B) {
	for _, n := range []int{10, 1000, 5000} {
		b.Run(fmt.Sprintf("lines=%d", n), func(b *testing.B) {
			m := NewPresenter(nil, nil, "").HandleResize(80, 30)
			for range 100 {
				m = m.AppendSystem("history entry with enough content to populate the viewport")
			}
			text := strings.Repeat("2026-11-06 INFO request completed method=GET path=/api/rooms status=200 duration=12ms\n", n-1) + "tail"
			m = m.SetComposeValue(text)
			if m.ComposeValue() != text {
				b.Fatal("buffer was truncated")
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				k := tea.Key{Code: 'x', Text: "x"}
				if i%2 == 1 {
					k = tea.Key{Code: tea.KeyBackspace}
				}
				m, _ = m.Update(tea.KeyPressMsg(k))
				_ = m.View()
			}
		})
	}
}

func BenchmarkComposeHeightChange(b *testing.B) {
	m := NewPresenter(nil, nil, "").HandleResize(80, 30)
	for range 100 {
		m = m.AppendSystem("history entry with enough content to populate the viewport")
	}
	m = m.SetComposeValue("tail")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		k := tea.Key{Code: tea.KeyEnter, Mod: tea.ModAlt}
		if i%2 == 1 {
			k = tea.Key{Code: tea.KeyBackspace}
		}
		m, _ = m.Update(tea.KeyPressMsg(k))
		_ = m.View()
	}
}
