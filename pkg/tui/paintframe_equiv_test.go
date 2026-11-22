package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

func oldPaintFrame(m *Model, content string) string {
	placed := lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, content)
	return reapplyBackground(AppContainer.Width(m.width).Height(m.height).MaxHeight(m.height).Render(placed))
}

func TestPaintFrameEquivalence(t *testing.T) {
	old := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(old)

	m := buildBenchModel(t, 20)
	m.width = 200
	m.height = 50
	m.updateViewport()

	fitting := map[string]string{
		"viewport":   m.viewport.View(),
		"empty":      "",
		"ansi":       "\x1b[31mred\x1b[0m plain \x1b[1;42mbold green bg\x1b[0m",
		"singleline": "hello",
		"exactWidth": strings.Repeat("a", 200),
	}

	for name, inner := range fitting {
		current := m.paintFrame(inner)
		ref := oldPaintFrame(m, inner)
		if current != ref {
			t.Errorf("%s: output differs from old impl (new %d bytes, old %d bytes)", name, len(current), len(ref))
			for i := 0; i < len(current) && i < len(ref); i++ {
				if current[i] != ref[i] {
					t.Errorf("first diff at byte %d: new %q old %q", i, current[max(0, i-20):min(len(current), i+20)], ref[max(0, i-20):min(len(ref), i+20)])
					break
				}
			}
		}
	}
}

func TestPaintFrameOversized(t *testing.T) {
	old := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(old)

	m := buildBenchModel(t, 1)
	m.width = 200
	m.height = 50

	oversized := longContent(300, 80)
	out := m.paintFrame(oversized)

	lines := strings.Split(out, "\n")
	for i, ln := range lines {
		if w := ansi.StringWidth(ln); w > m.width {
			t.Fatalf("line %d exceeds frame width: %d > %d", i, w, m.width)
		}
	}
	if len(lines) > m.height {
		t.Fatalf("frame has %d lines, want <= %d", len(lines), m.height)
	}
}

func longContent(width, lines int) string {
	row := strings.Repeat("a", width)
	out := ""
	for i := 0; i < lines; i++ {
		if i > 0 {
			out += "\n"
		}
		out += row
	}
	return out
}
