package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestClampPaneLinePadsAndTruncates(t *testing.T) {
	got := clampPaneLine("ab", 5)
	if w := ansi.StringWidth(got); w != 5 {
		t.Fatalf("padded width = %d, want 5 (%q)", w, got)
	}
	if !strings.HasSuffix(got, "   ") {
		t.Fatalf("expected trailing spaces, got %q", got)
	}

	long := strings.Repeat("x", 10)
	got = clampPaneLine(long, 4)
	if w := ansi.StringWidth(got); w != 4 {
		t.Fatalf("truncated width = %d, want 4 (%q)", w, got)
	}

	withEmoji := strings.Repeat("a", 3) + "🌐" + strings.Repeat("b", 5)
	got = clampPaneLine(withEmoji, 4)
	if w := ansi.StringWidth(got); w > 4 {
		t.Fatalf("emoji line width = %d, want <=4 (%q)", w, got)
	}
}

func TestClampPaneLinesUniformWidth(t *testing.T) {
	pane := "short\n" + strings.Repeat("z", 50) + "\n"
	out := clampPaneLines(pane, 20)
	for i, line := range strings.Split(out, "\n") {
		if w := ansi.StringWidth(line); w != 20 {
			t.Fatalf("line %d width = %d, want 20 (%q)", i, w, line)
		}
	}
}
