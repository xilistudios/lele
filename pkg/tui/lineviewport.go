package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// lineViewport is a lightweight replacement for bubbles/viewport.Model optimized
// for large chat histories. The standard viewport.SetContent() does O(n) work
// on every call (strings.Split + findLongestLineWidth with ansi.StringWidth on
// every line). For conversations with thousands of rendered lines, this dominates
// CPU usage — especially during streaming when SetContent is called every32ms.
//
// lineViewport stores lines as []string directly (no Split needed) and only
// processes the visible window in View(). During streaming, callers can use
// SetBaseLines + SetOverlayLines to avoid rebuilding the entire line buffer.
type lineViewport struct {
	Width  int
	Height int
	Style  lipgloss.Style

	// YOffset is the scroll position (line index of topmost visible line).
	YOffset int

	MouseWheelEnabled bool
	MouseWheelDelta   int

	// baseLines are the committed history lines. Only rebuilt when messages change.
	baseLines []string
	// overlayLines are ephemeral lines (streaming, approvals, feedback) appended
	// after baseLines. Rebuilt every frame during streaming — but they're small.
	overlayLines []string
	// longestLineWidth caches the widest line to avoid recomputing on every View().
	// Updated incrementally when baseLines/overlayLines change.
	longestLineWidth int

	// Pre-allocated style for View() rendering — avoids creating a new
	// lipgloss.Style on every frame (which triggers GC pressure).
	viewStyle  lipgloss.Style
	viewStyleW int
	viewStyleH int
}

func newLineViewport(width, height int) lineViewport {
	return lineViewport{
		Width:             width,
		Height:            height,
		MouseWheelEnabled: true,
		MouseWheelDelta:   3,
	}
}

// SetBaseLines replaces the base (history) lines. Call only when message count
// or width changes. O(1) pointer swap + O(n) width scan (unavoidable for
// correct horizontal scroll, but amortized by the caller's cache).
func (v *lineViewport) SetBaseLines(lines []string) {
	v.baseLines = lines
	v.recalcLongestWidth()
	v.clampOffset()
}

// SetOverlayLines replaces the ephemeral overlay lines (streaming, approvals).
// Called every frame during streaming but the overlay is small (typically <20 lines).
func (v *lineViewport) SetOverlayLines(lines []string) {
	v.overlayLines = lines
	v.recalcLongestWidth()
	v.clampOffset()
}

// SetContent provides backward compatibility with viewport.Model. Splits the
// string into lines and sets them as baseLines. Prefer SetBaseLines for new code.
func (v *lineViewport) SetContent(s string) {
	if s == "" {
		v.baseLines = nil
		v.overlayLines = nil
		v.longestLineWidth = 0
		return
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	v.baseLines = strings.Split(s, "\n")
	v.overlayLines = nil
	v.recalcLongestWidth()
	v.clampOffset()
}

// totalLines returns the combined line count.
func (v *lineViewport) totalLines() int {
	return len(v.baseLines) + len(v.overlayLines)
}

// lineAt returns the line at the given global index, pulling from baseLines
// or overlayLines as needed. Returns ("", false) if out of range.
// This avoids the O(n) allocation of allLines() — callers only touch the
// specific indices they need.
func (v *lineViewport) lineAt(i int) (string, bool) {
	if i < 0 {
		return "", false
	}
	if i < len(v.baseLines) {
		return v.baseLines[i], true
	}
	j := i - len(v.baseLines)
	if j < len(v.overlayLines) {
		return v.overlayLines[j], true
	}
	return "", false
}

// visibleLines returns only the lines in the current scroll window. This is
// O(visible) instead of O(total) — the key performance win over the standard
// viewport which processes all lines in SetContent.
//
// Unlike the previous implementation, this does NOT call allLines() and
// therefore does NOT allocate a combined slice on every frame during streaming.
func (v *lineViewport) visibleLines() []string {
	total := v.totalLines()
	if total == 0 {
		return nil
	}

	h := v.Height - v.Style.GetVerticalFrameSize()
	if h <= 0 {
		return nil
	}

	top := max(0, v.YOffset)
	bottom := min(top+h, total)
	if bottom <= top {
		return nil
	}

	// Build only the visible lines directly from base + overlay.
	result := make([]string, 0, bottom-top)
	for i := top; i < bottom; i++ {
		if line, ok := v.lineAt(i); ok {
			result = append(result, line)
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

// View renders only the visible portion. O(visible_lines) — typically 30-50.
// Uses a cached lipgloss.Style to avoid allocating a new one on every frame.
func (v *lineViewport) View() string {
	w := v.Width - v.Style.GetHorizontalFrameSize()
	h := v.Height - v.Style.GetVerticalFrameSize()
	if w <= 0 || h <= 0 {
		return ""
	}

	visible := v.visibleLines()
	if len(visible) == 0 {
		// Cache the empty-content style
		if v.viewStyleW != w || v.viewStyleH != h {
			v.viewStyle = lipgloss.NewStyle().Width(w).Height(h).MaxHeight(h).MaxWidth(w)
			v.viewStyleW = w
			v.viewStyleH = h
		}
		return v.viewStyle.Render("")
	}

	// Cache the content style (only recreated when dimensions change)
	if v.viewStyleW != w || v.viewStyleH != h {
		v.viewStyle = lipgloss.NewStyle().Width(w).Height(h).MaxHeight(h).MaxWidth(w)
		v.viewStyleW = w
		v.viewStyleH = h
	}

	contents := v.viewStyle.Render(strings.Join(visible, "\n"))

	return v.Style.
		UnsetWidth().UnsetHeight().
		Render(contents)
}

// AtBottom reports whether the viewport is scrolled to the bottom.
func (v *lineViewport) AtBottom() bool {
	return v.YOffset >= v.maxYOffset()
}

// AtTop reports whether the viewport is scrolled to the top.
func (v *lineViewport) AtTop() bool {
	return v.YOffset <= 0
}

// GotoBottom scrolls to the last line.
func (v *lineViewport) GotoBottom() {
	v.YOffset = v.maxYOffset()
}

// GotoTop scrolls to the first line.
func (v *lineViewport) GotoTop() {
	v.YOffset = 0
}

// maxYOffset returns the maximum YOffset.
func (v *lineViewport) maxYOffset() int {
	h := v.Height - v.Style.GetVerticalFrameSize()
	return max(0, v.totalLines()-h)
}

// clampOffset ensures YOffset is within valid bounds.
func (v *lineViewport) clampOffset() {
	maxY := v.maxYOffset()
	if v.YOffset > maxY {
		v.YOffset = maxY
	}
	if v.YOffset < 0 {
		v.YOffset = 0
	}
}

// recalcLongestWidth computes the widest line for horizontal scroll support.
// We skip the expensive ansi.StringWidth scan on base lines because:
// 1. We never use horizontal scrolling (xOffset is always 0)
// 2. Content is word-wrapped to viewport width
// Only overlay lines are scanned (they're small).
func (v *lineViewport) recalcLongestWidth() {
	w := 0
	for _, l := range v.overlayLines {
		if ww := ansi.StringWidth(l); ww > w {
			w = ww
		}
	}
	// Base lines: use a cheap estimate (viewport width) instead of ANSI parsing.
	// This is correct because we word-wrap to viewport.Width.
	if v.Width > w {
		w = v.Width
	}
	v.longestLineWidth = w
}

// Update handles mouse wheel events for scrolling.
func (v *lineViewport) Update(msg tea.Msg) (lineViewport, tea.Cmd) {
	if !v.MouseWheelEnabled {
		return *v, nil
	}

	mouseMsg, ok := msg.(tea.MouseMsg)
	if !ok {
		return *v, nil
	}

	switch mouseMsg.Action {
	case tea.MouseActionPress:
		switch mouseMsg.Button {
		case tea.MouseButtonWheelUp:
			lines := min(v.YOffset, v.MouseWheelDelta)
			v.YOffset -= lines
		case tea.MouseButtonWheelDown:
			v.YOffset += v.MouseWheelDelta
			v.clampOffset()
		}
	}

	return *v, nil
}

// ScrollPercent returns 0..1 scroll position.
func (v *lineViewport) ScrollPercent() float64 {
	total := v.totalLines()
	if v.Height >= total {
		return 1.0
	}
	denom := float64(total - v.Height)
	if denom <= 0 {
		return 1.0
	}
	pct := float64(v.YOffset) / denom
	if pct < 0 {
		pct = 0
	}
	if pct > 1 {
		pct = 1
	}
	return pct
}
