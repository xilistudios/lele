package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

// TestSidebarLabelValueFitsLocaleLabels guards the sidebar metric row layout.
// Spanish "Ventana de contexto" is 19 cells and used to overflow the old
// %-18s pad: the value shifted one column and, on narrow sidebars, that
// single row wrapped and pushed the rest of the panel down.
func TestSidebarLabelValueFitsLocaleLabels(t *testing.T) {
	labels := []string{
		"Current context",     // en
		"Context window",      // en
		"Ventana de contexto", // es — longest shipped label (19)
		"Janela de contexto",  // pt
		"Contexto actual",
		"Compactações",
	}

	for _, label := range labels {
		if w := lipgloss.Width(label); w > sidebarLabelWidth {
			t.Fatalf("label %q is %d cells; sidebarLabelWidth=%d must be raised",
				label, w, sidebarLabelWidth)
		}

		row := SidebarLabelValue(label, "1,234")
		// Value must start on the same column for every label.
		plain := lipgloss.Width(row)
		want := sidebarLabelWidth + 1 /* PaddingLeft */ + lipgloss.Width("1,234")
		if plain != want {
			t.Errorf("SidebarLabelValue(%q) width = %d, want %d (label must pad to %d)",
				label, plain, want, sidebarLabelWidth)
		}
	}
}

// TestSidebarLabelValueTruncatesOverlongLabel ensures a future locale string
// longer than the column is cut instead of wrapping.
func TestSidebarLabelValueTruncatesOverlongLabel(t *testing.T) {
	long := strings.Repeat("W", sidebarLabelWidth+8)
	row := SidebarLabelValue(long, "9")
	if got := lipgloss.Width(row); got != sidebarLabelWidth+1+1 {
		t.Fatalf("overlong row width = %d, want %d (no wrap)",
			got, sidebarLabelWidth+1+1)
	}
	if !strings.Contains(row, "...") {
		t.Fatalf("expected ellipsis in truncated row, got %q", row)
	}
}

// TestSidebarMetricRowsDoNotWrapAtNarrowContentWidth reproduces the layout
// shift: at the measurement width used by view.go (contentWidth), every
// Spanish metric row must stay a single line even with a wide token count.
func TestSidebarMetricRowsDoNotWrapAtNarrowContentWidth(t *testing.T) {
	// contentWidth for a ~120-col terminal: rightWidth=31 → contentWidth=27.
	// The old 19-char label + pad + a 9-digit value wrapped and shifted
	// Workspace/Estado down by one line.
	const contentWidth = 27

	var b strings.Builder
	for _, tc := range []struct{ label, value string }{
		{"Contexto actual", "28,482"},
		{"Ventana de contexto", "1,220,000"}, // 9-digit value, previously wrapped
		{"Entrada enviada", "155,030"},
		{"Salida recibida", "1,567"},
		{"Total enviado", "156,597"},
		{"Compacciones", "0"},
	} {
		b.WriteString(clampSidebarRow(SidebarLabelValue(tc.label, tc.value), contentWidth) + "\n")
	}

	rendered := lipgloss.NewStyle().Width(contentWidth).Render(b.String())
	raw := strings.Split(rendered, "\n")
	lines := make([]string, 0, len(raw))
	for _, line := range raw {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	if len(lines) != 6 {
		t.Fatalf("expected 6 sidebar metric lines, got %d:\n%q", len(lines), rendered)
	}
	for i, line := range lines {
		if lipgloss.Width(line) > contentWidth {
			t.Fatalf("line %d exceeds contentWidth: %q (%d cells)", i, line, lipgloss.Width(line))
		}
	}
}

// TestSidebarSectionSpacingIsSingleBlankLine guards against MarginTop/Bottom
// stacking with the explicit newlines in view.go. A double blank between
// Compacciones and Workspace made the panel taller than the height budget so
// the bottom sections jumped when anything was clipped.
func TestSidebarSectionSpacingIsSingleBlankLine(t *testing.T) {
	var b strings.Builder
	b.WriteString(SidebarTitle.Render("session") + "\n\n")
	b.WriteString(SidebarHeader.Render("Contexto") + "\n")
	b.WriteString(SidebarLabelValue("Compacciones", "0") + "\n")
	b.WriteString("\n")
	b.WriteString(SidebarHeader.Render("Workspace") + "\n")
	b.WriteString(SidebarValue.Render("/tmp/ws") + "\n")
	b.WriteString("\n")
	b.WriteString(SidebarHeader.Render("Estado") + "\n")
	b.WriteString(" " + SidebarConnectedDot.Render("●") + " Lele 0.0.0\n")

	lines := strings.Split(b.String(), "\n")
	// Find "Compacciones" and "Workspace"; exactly one blank between them.
	compIdx, wsIdx := -1, -1
	for i, line := range lines {
		plain := stripANSI(line)
		if strings.Contains(plain, "Compacciones") {
			compIdx = i
		}
		if strings.TrimSpace(plain) == "Workspace" {
			wsIdx = i
		}
	}
	if compIdx < 0 || wsIdx < 0 {
		t.Fatalf("missing section markers: comp=%d ws=%d\n%q", compIdx, wsIdx, b.String())
	}
	between := lines[compIdx+1 : wsIdx]
	blanks := 0
	for _, line := range between {
		if strings.TrimSpace(stripANSI(line)) == "" {
			blanks++
		} else {
			t.Fatalf("unexpected content between sections: %q", line)
		}
	}
	if blanks != 1 {
		t.Fatalf("expected 1 blank line between Compacciones and Workspace, got %d\n%q", blanks, b.String())
	}
}

// TestSidebarVersionLineKeepsForegroundAfterDot: nested SidebarValue.Render(
// SidebarConnectedDot.Render("●") + ...) emitted an inner SGR reset that left
// "Lele <ver>" on the terminal default (the intermittent colour shift).
func TestSidebarVersionLineKeepsForegroundAfterDot(t *testing.T) {
	forceTrueColor(t)
	line := " " + SidebarConnectedDot.Render("●") +
		lipgloss.NewStyle().Foreground(Foreground).Render(" Lele 0.0.0")
	if strings.Count(line, "\x1b[0m") < 2 {
		t.Fatalf("expected dot and text each to close their own SGR run, got %q", line)
	}
	// After the dot's reset the text run must re-open a foreground.
	dotReset := strings.Index(line, "\x1b[0m")
	rest := line[dotReset+4:]
	if !strings.HasPrefix(rest, "\x1b[") {
		t.Fatalf("text after dot reset must open a new SGR, got %q", rest)
	}
}
