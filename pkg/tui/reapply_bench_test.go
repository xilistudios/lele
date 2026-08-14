package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// buildFakeFrame simulates a rendered full-screen TUI frame: many lines of
// lipgloss-styled text, each styled run terminated by a reset.
func buildFakeFrame(lines, cols int) string {
	var sb strings.Builder
	for i := 0; i < lines; i++ {
		// ~6 styled runs per line (role labels, message text, borders...)
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
		{50, 120},   // small terminal
		{200, 120},  // typical
		{500, 200},  // large conversation
		{1000, 200}, // very long session
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
