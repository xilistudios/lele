package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func NewLVForTest(width, height int) lineViewport {
	return newLineViewport(width, height)
}

func TestLineViewportSetContentEmpty(t *testing.T) {
	v := newLineViewport(80, 10)
	v.SetContent("")
	if v.totalLines() != 0 {
		t.Errorf("totalLines = %d, want 0", v.totalLines())
	}
	if v.longestLineWidth != 0 {
		t.Errorf("longestLineWidth = %d, want 0", v.longestLineWidth)
	}
}

func TestLineViewportSetContentSplitsCRLF(t *testing.T) {
	v := newLineViewport(80, 10)
	v.SetContent("a\r\nb\nc")
	if v.totalLines() != 3 {
		t.Fatalf("totalLines = %d, want 3", v.totalLines())
	}
	line, ok := v.lineAt(1)
	if !ok || line != "b" {
		t.Errorf("lineAt(1) = %q, %v", line, ok)
	}
}

func TestLineViewportLineAtBounds(t *testing.T) {
	v := newLineViewport(80, 10)
	v.SetBaseLines([]string{"a", "b"})
	v.SetOverlayLines([]string{"x"})

	if _, ok := v.lineAt(-1); ok {
		t.Error("lineAt(-1) should be invalid")
	}
	if l, ok := v.lineAt(0); !ok || l != "a" {
		t.Errorf("lineAt(0) = %q, %v", l, ok)
	}
	if l, ok := v.lineAt(2); !ok || l != "x" {
		t.Errorf("lineAt(2) = %q, %v", l, ok)
	}
	if _, ok := v.lineAt(3); ok {
		t.Error("lineAt(3) should be invalid")
	}
}

func TestLineViewportScrollAndClamp(t *testing.T) {
	v := newLineViewport(80, 3)
	lines := make([]string, 10)
	for i := range lines {
		lines[i] = "line"
	}
	v.SetBaseLines(lines)
	// Height 3 -> maxYOffset 7
	if got := v.maxYOffset(); got != 7 {
		t.Errorf("maxYOffset = %d, want 7", got)
	}

	v.YOffset = 100
	v.clampOffset()
	if v.YOffset != 7 {
		t.Errorf("clampOffset top = %d, want 7", v.YOffset)
	}

	v.YOffset = -5
	v.clampOffset()
	if v.YOffset != 0 {
		t.Errorf("clampOffset bottom = %d, want 0", v.YOffset)
	}

	if v.AtTop() != true {
		t.Error("expected AtTop true at offset 0")
	}
	v.GotoBottom()
	if !v.AtBottom() {
		t.Error("expected AtBottom after GotoBottom")
	}
	if v.ScrollPercent() != 1.0 {
		t.Errorf("ScrollPercent = %v, want 1", v.ScrollPercent())
	}
	// Content fits -> ScrollPercent 1.
	v2 := newLineViewport(80, 10)
	v2.SetBaseLines([]string{"a", "b"})
	if v2.ScrollPercent() != 1.0 {
		t.Errorf("short viewport ScrollPercent = %v", v2.ScrollPercent())
	}
}

func TestLineViewportPageHeight(t *testing.T) {
	v := newLineViewport(80, 5)
	if got := v.pageHeight(); got != 5 {
		t.Errorf("pageHeight = %d, want 5", got)
	}
	// Height 0 -> at least 1.
	v2 := newLineViewport(80, 0)
	if got := v2.pageHeight(); got != 1 {
		t.Errorf("pageHeight zero = %d, want 1", got)
	}
}

func TestLineViewportUpdateKeys(t *testing.T) {
	forceTrueColor(t)
	v := newLineViewport(80, 3)
	lines := make([]string, 10)
	for i := range lines {
		lines[i] = "line"
	}
	v.SetBaseLines(lines)
	v.GotoTop()

	// down
	v, _ = v.Update(tea.KeyMsg{Type: tea.KeyDown})
	if v.YOffset != 1 {
		t.Errorf("down offset = %d", v.YOffset)
	}
	// up
	v, _ = v.Update(tea.KeyMsg{Type: tea.KeyUp})
	if v.YOffset != 0 {
		t.Errorf("up offset = %d", v.YOffset)
	}
	// pgdown
	v, _ = v.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	if v.YOffset != 3 {
		t.Errorf("pgdown offset = %d", v.YOffset)
	}
	// pgup
	v, _ = v.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	if v.YOffset != 0 {
		t.Errorf("pgup offset = %d", v.YOffset)
	}
}

func TestLineViewportUpdateKeyByString(t *testing.T) {
	v := newLineViewport(80, 3)
	lines := make([]string, 10)
	for i := range lines {
		lines[i] = "line"
	}
	v.SetBaseLines(lines)
	v.GotoTop()
	v, _ = v.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")}) // no match
	if v.YOffset != 0 {
		t.Errorf("unmatched key should not scroll: %d", v.YOffset)
	}
}

func TestLineViewportMouseWheel(t *testing.T) {
	forceTrueColor(t)
	v := newLineViewport(80, 3)
	lines := make([]string, 10)
	for i := range lines {
		lines[i] = "line"
	}
	v.SetBaseLines(lines)
	v.GotoBottom()
	// Wheel up should decrease offset.
	before := v.YOffset
	v, _ = v.Update(tea.MouseMsg{
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonWheelUp,
	})
	if v.YOffset >= before {
		t.Errorf("wheel up should scroll up, was %d now %d", before, v.YOffset)
	}
	// Wheel down increases offset then clamps.
	v, _ = v.Update(tea.MouseMsg{
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonWheelDown,
	})
	if !v.AtBottom() {
		t.Errorf("wheel down to bottom, offset %d max %d", v.YOffset, v.maxYOffset())
	}

	// Disabled mouse.
	v.MouseWheelEnabled = false
	before = v.YOffset
	v, _ = v.Update(tea.MouseMsg{
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonWheelUp,
	})
	if v.YOffset != before {
		t.Error("wheel should be ignored when disabled")
	}
}

func TestLineViewportViewRendering(t *testing.T) {
	forceTrueColor(t)
	v := newLineViewport(80, 10)
	v.SetContent("line one\nline two")
	out := v.View()
	if out == "" {
		t.Error("expected non-empty rendered output")
	}

	// Zero height returns empty.
	v0 := newLineViewport(0, 0)
	if out := v0.View(); out != "" {
		t.Errorf("expected empty View for zero dims, got %q", out)
	}
}

func TestLineViewportVisibleLines(t *testing.T) {
	v := newLineViewport(80, 3)
	// Empty -> nil
	if v.visibleLines() != nil {
		t.Error("expected nil visible lines for empty viewport")
	}
	v.SetBaseLines([]string{"a", "b", "c", "d", "e"})
	got := v.visibleLines()
	if len(got) != 3 {
		t.Errorf("visibleLines len = %d, want 3", len(got))
	}
	if got[0] != "a" {
		t.Errorf("first visible = %q", got[0])
	}
	// Scroll down.
	v.YOffset = 2
	got = v.visibleLines()
	if got[0] != "c" {
		t.Errorf("after scroll first visible = %q", got[0])
	}
}

func TestLineViewportRecalcLongestWidthOverlay(t *testing.T) {
	v := newLineViewport(80, 10)
	v.SetOverlayLines([]string{"short", string(make([]byte, 100))})
	if v.longestLineWidth < 80 {
		t.Errorf("longestLineWidth = %d, want >= 80", v.longestLineWidth)
	}
}

func TestCachedStylesConsistency(t *testing.T) {
	forceTrueColor(t)
	v := newLineViewport(80, 10)
	v.SetContent("a\nb")
	v.View() // populate cache
	if v.viewStyleW != v.Width || v.viewStyleH != v.Height {
		t.Errorf("view style cache dims mismatch: %d/%d", v.viewStyleW, v.viewStyleH)
	}
}
