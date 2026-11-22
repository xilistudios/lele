package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestSidebarLabelValueFitsLocaleLabels(t *testing.T) {
	labels := []string{
		"Current context",
		"Context window",
		"Ventana de contexto",
		"Janela de contexto",
		"Contexto actual",
		"Compactações",
	}

	for _, label := range labels {
		if w := lipgloss.Width(label); w > sidebarLabelWidth {
			t.Fatalf("label %q is %d cells; sidebarLabelWidth=%d must be raised",
				label, w, sidebarLabelWidth)
		}

		row := SidebarLabelValue(label, "1,234")
		plain := lipgloss.Width(row)
		want := sidebarLabelWidth + 1 + lipgloss.Width("1,234")
		if plain != want {
			t.Errorf("SidebarLabelValue(%q) width = %d, want %d (label must pad to %d)",
				label, plain, want, sidebarLabelWidth)
		}
	}
}

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

func TestSidebarMetricRowsDoNotWrapAtNarrowContentWidth(t *testing.T) {
	const contentWidth = 27

	var b strings.Builder
	for _, tc := range []struct{ label, value string }{
		{"Contexto actual", "28,482"},
		{"Ventana de contexto", "1,220,000"},
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

func TestSidebarVersionLineKeepsForegroundAfterDot(t *testing.T) {
	forceTrueColor(t)
	line := " " + SidebarConnectedDot.Render("●") +
		lipgloss.NewStyle().Foreground(Foreground).Render(" Lele 0.0.0")
	if strings.Count(line, "\x1b[0m") < 2 {
		t.Fatalf("expected dot and text each to close their own SGR run, got %q", line)
	}
	dotReset := strings.Index(line, "\x1b[0m")
	rest := line[dotReset+4:]
	if !strings.HasPrefix(rest, "\x1b[") {
		t.Fatalf("text after dot reset must open a new SGR, got %q", rest)
	}
}
