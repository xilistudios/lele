package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

type lineViewport struct {
	Width  int
	Height int
	Style  lipgloss.Style

	YOffset int

	MouseWheelEnabled bool
	MouseWheelDelta   int

	baseLines        []string
	overlayLines     []string
	longestLineWidth int

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

func (v *lineViewport) SetBaseLines(lines []string) {
	v.baseLines = lines
	v.recalcLongestWidth()
	v.clampOffset()
}

func (v *lineViewport) SetOverlayLines(lines []string) {
	v.overlayLines = lines
	v.recalcLongestWidth()
	v.clampOffset()
}

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

func (v *lineViewport) totalLines() int {
	return len(v.baseLines) + len(v.overlayLines)
}

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

func (v *lineViewport) View() string {
	w := v.Width - v.Style.GetHorizontalFrameSize()
	h := v.Height - v.Style.GetVerticalFrameSize()
	if w <= 0 || h <= 0 {
		return ""
	}

	visible := v.visibleLines()
	if len(visible) == 0 {
		if v.viewStyleW != w || v.viewStyleH != h {
			v.viewStyle = lipgloss.NewStyle().Width(w).Height(h).MaxHeight(h).MaxWidth(w)
			v.viewStyleW = w
			v.viewStyleH = h
		}
		return v.viewStyle.Render("")
	}

	for i, l := range visible {
		if ansi.StringWidth(l) > w {
			visible[i] = closeOpenSGR(ansi.Truncate(l, w, ""))
		}
	}

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

func (v *lineViewport) AtBottom() bool {
	return v.YOffset >= v.maxYOffset()
}

func (v *lineViewport) AtTop() bool {
	return v.YOffset <= 0
}

func (v *lineViewport) GotoBottom() {
	v.YOffset = v.maxYOffset()
}

func (v *lineViewport) GotoTop() {
	v.YOffset = 0
}

func (v *lineViewport) maxYOffset() int {
	h := v.Height - v.Style.GetVerticalFrameSize()
	return max(0, v.totalLines()-h)
}

func (v *lineViewport) clampOffset() {
	maxY := v.maxYOffset()
	if v.YOffset > maxY {
		v.YOffset = maxY
	}
	if v.YOffset < 0 {
		v.YOffset = 0
	}
}

func (v *lineViewport) recalcLongestWidth() {
	w := 0
	for _, l := range v.overlayLines {
		if ww := ansi.StringWidth(l); ww > w {
			w = ww
		}
	}
	if v.Width > w {
		w = v.Width
	}
	v.longestLineWidth = w
}

func (v *lineViewport) pageHeight() int {
	h := v.Height - v.Style.GetVerticalFrameSize()
	if h < 1 {
		h = 1
	}
	return h
}

func (v *lineViewport) Update(msg tea.Msg) (lineViewport, tea.Cmd) {
	switch m := msg.(type) {
	case tea.MouseMsg:
		if !v.MouseWheelEnabled {
			return *v, nil
		}
		switch m.Action {
		case tea.MouseActionPress:
			switch m.Button {
			case tea.MouseButtonWheelUp:
				lines := min(v.YOffset, v.MouseWheelDelta)
				v.YOffset -= lines
			case tea.MouseButtonWheelDown:
				v.YOffset += v.MouseWheelDelta
				v.clampOffset()
			}
		}
	case tea.KeyMsg:
		switch m.String() {
		case "up":
			v.YOffset--
			v.clampOffset()
		case "down":
			v.YOffset++
			v.clampOffset()
		case "pgup":
			v.YOffset -= v.pageHeight()
			v.clampOffset()
		case "pgdown":
			v.YOffset += v.pageHeight()
			v.clampOffset()
		}
	}

	return *v, nil
}

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
