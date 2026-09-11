package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// TestClampMarkdownWidth_CutsUnbreakableTokens: glamour's WithWordWrap does
// not cut long paths/URLs. Those overflow lines paint over the sidebar border
// on scroll (Terminal.app shows a short Accent-coloured bar).
func TestClampMarkdownWidth_CutsUnbreakableTokens(t *testing.T) {
	longPath := "/Users/alfredo/Documents/Development/xilistudios/lele/pkg/tui/view.go/and/more"
	line := "\x1b[38;2;139;233;253m" + longPath + "\x1b[0m"
	if ansi.StringWidth(line) <= 40 {
		t.Fatalf("test line too short: %d", ansi.StringWidth(line))
	}
	out := clampMarkdownWidth(line, 40)
	if w := ansi.StringWidth(out); w > 40 {
		t.Fatalf("clamped width = %d, want <=40", w)
	}
	if !strings.Contains(out, "/Users") {
		t.Fatalf("expected prefix kept, got %q", out)
	}
}

func TestClampMarkdownWidth_EmptyAndShortUnchanged(t *testing.T) {
	if got := clampMarkdownWidth("", 40); got != "" {
		t.Fatalf("empty = %q", got)
	}
	short := "hello"
	if got := clampMarkdownWidth(short, 40); got != short {
		t.Fatalf("short changed: %q", got)
	}
}

// TestRenderMarkdown_LongPathDoesNotExceedWidth is the end-to-end guard:
// a real filesystem path in markdown must not produce a line wider than the
// requested render width (glamour would leave it unwrapped).
func TestRenderMarkdown_LongPathDoesNotExceedWidth(t *testing.T) {
	m := newTestModel(t)
	const width = 50
	content := "## 🌐 Red\n\n- Path: /Users/alfredo/Documents/Development/xilistudios/lele/pkg/tui/view.go/very/long/tail\n"
	out := m.renderMarkdown(content, width)
	for i, line := range strings.Split(out, "\n") {
		if w := ansi.StringWidth(line); w > width {
			t.Fatalf("line %d width=%d > %d: %q", i, w, width, ansi.Strip(line))
		}
	}
}
