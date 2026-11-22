package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

func buildFakeFrame(lines, cols int) string {
	var sb strings.Builder
	for i := 0; i < lines; i++ {
		for j := 0; j < 6; j++ {
			sb.WriteString("\x1b[38;2;80;250;123m")
			sb.WriteString(strings.Repeat("x", cols/6))
			sb.WriteString("\x1b[0m")
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

var sink string

func BenchmarkReapplyBackground(b *testing.B) {
	old := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(old)

	for _, size := range []struct{ lines, cols int }{
		{50, 120},
		{200, 120},
		{500, 200},
		{1000, 200},
	} {
		frame := buildFakeFrame(size.lines, size.cols)
		b.Run(fmt.Sprintf("%dx%d", size.lines, size.cols), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				sink = reapplyBackground(frame)
			}
		})
	}
}
