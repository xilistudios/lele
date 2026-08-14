package tui

import (
	"fmt"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

var placeSink string

// BenchmarkPaintFrameVsPlace isolates the paintFrame overhead (Place +
// AppContainer render + reapplyBackground) against the pre-#188 behavior
// (Place + AppContainer render only).
func BenchmarkPaintFrameVsPlace(b *testing.B) {
	old := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(old)

	m := buildBenchModel(b, 20)
	m.width = 200
	m.height = 50

	// Render the chat layout once WITHOUT the final paintFrame step by
	// capturing what View() produces and stripping the outer container.
	// Simpler: render a representative inner layout via the real viewport.
	m.updateViewport()
	inner := m.viewport.View()

	b.Run("pre188_Place+AppContainer", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			placed := lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, inner)
			placeSink = AppContainer.Width(m.width).Height(m.height).MaxHeight(m.height).Render(placed)
		}
	})

	b.Run("post188_paintFrame", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			placeSink = m.paintFrame(inner)
		}
	})
}

func TestPaintFrameOverhead(t *testing.T) {
	old := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(old)

	m := buildBenchModel(t, 20)
	m.width = 200
	m.height = 50
	m.updateViewport()
	inner := m.viewport.View()

	t0 := testing.AllocsPerRun(50, func() {
		placed := lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, inner)
		placeSink = AppContainer.Width(m.width).Height(m.height).MaxHeight(m.height).Render(placed)
	})
	t1 := testing.AllocsPerRun(50, func() {
		placeSink = m.paintFrame(inner)
	})
	fmt.Printf("pre188 allocs/op: %.0f\npost188 allocs/op: %.0f\n", t0, t1)
}
