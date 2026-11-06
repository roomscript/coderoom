package compose

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func BenchmarkKeystroke(b *testing.B) {
	for _, n := range []int{10, 1000, 5000} {
		b.Run(fmt.Sprintf("lines=%d", n), func(b *testing.B) {
			text := strings.Repeat("2026-11-06 INFO request completed method=GET path=/api/rooms status=200 duration=12ms\n", n-1) + "tail"
			m := New().SetWidth(80).SetMaxHeightFromTotal(30).SetValue(text)
			if m.Value() != text {
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

// BenchmarkView isolates viewport rendering from textarea's Update, which
// currently performs its own full-buffer render internally.
func BenchmarkView(b *testing.B) {
	for _, n := range []int{10, 1000, 5000} {
		b.Run(fmt.Sprintf("lines=%d", n), func(b *testing.B) {
			m := New().SetWidth(80).SetMaxHeightFromTotal(30).SetValue(strings.Repeat("log entry with words to wrap across the terminal width and inspect\n", n-1) + "tail")
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				_ = m.View()
			}
		})
	}
}
