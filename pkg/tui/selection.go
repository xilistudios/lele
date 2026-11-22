package tui

import (
	"regexp"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

var ansiEscapeRe = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]|\x1b\][^\x07]*\x07|\x1b[()][0-9A-B]|\x1b[=>]`)

func stripANSI(s string) string {
	return ansiEscapeRe.ReplaceAllString(s, "")
}

var SelectionStyle lipgloss.Style

func (m *Model) startSelection(x, y int) {
	leftWidth := int(float64(m.width) * leftColumnRatio)
	if x >= leftWidth-1 || y >= m.viewport.Height {
		return
	}
	m.selecting = true
	m.selStartX = x
	m.selStartY = y
	m.selEndX = x
	m.selEndY = y
}

func (m *Model) updateSelection(x, y int) {
	if !m.selecting {
		return
	}
	if y < 0 {
		y = 0
	}
	if y >= m.viewport.Height {
		y = m.viewport.Height - 1
	}
	if x < 0 {
		x = 0
	}
	if m.viewport.Width > 0 && x >= m.viewport.Width {
		x = m.viewport.Width - 1
	}
	m.selEndX = x
	m.selEndY = y
}

func (m *Model) normalizeSelection() (startY, startX, endY, endX int) {
	startY = m.selStartY
	startX = m.selStartX
	endY = m.selEndY
	endX = m.selEndX

	if startY > endY {
		startY, endY = endY, startY
		startX, endX = endX, startX
	} else if startY == endY && startX > endX {
		startX, endX = endX, startX
	}
	return
}

func (m *Model) isPointSelection() bool {
	return m.selStartX == m.selEndX && m.selStartY == m.selEndY
}

func (m *Model) finishSelection() {
	if !m.selecting {
		return
	}
	m.selecting = false

	startY, startX, endY, endX := m.normalizeSelection()

	text := m.extractSelectionText(startY, startX, endY, endX)
	if text == "" {
		return
	}

	copyToClipboard(text)
	m.selectionFeedback = true
	m.selectionFeedbackAt = time.Now()
}

func (m *Model) extractSelectionText(startY, startX, endY, endX int) string {
	content := m.viewport.View()
	visibleLines := strings.Split(content, "\n")

	if startY >= len(visibleLines) {
		return ""
	}
	if endY >= len(visibleLines) {
		endY = len(visibleLines) - 1
	}

	if m.isPointSelection() {
		plain := stripANSI(visibleLines[startY])
		return strings.TrimRight(plain, " ")
	}

	var sb strings.Builder

	if startY == endY {
		plain := stripANSI(visibleLines[startY])
		_, selected, _ := splitByColumns(plain, startX, endX)
		sb.WriteString(strings.TrimRight(selected, " "))
	} else {
		firstPlain := stripANSI(visibleLines[startY])
		_, firstSelected, _ := splitByColumns(firstPlain, startX, len(firstPlain)+1)
		sb.WriteString(strings.TrimRight(firstSelected, " "))
		sb.WriteString("\n")

		for i := startY + 1; i < endY; i++ {
			plain := stripANSI(visibleLines[i])
			sb.WriteString(strings.TrimRight(plain, " "))
			sb.WriteString("\n")
		}

		lastPlain := stripANSI(visibleLines[endY])
		lastSelected, _, _ := splitByColumns(lastPlain, 0, endX)
		sb.WriteString(strings.TrimRight(lastSelected, " "))
	}

	return strings.TrimRight(sb.String(), "\n")
}

func (m *Model) clearSelectionFeedback() {
	if m.selectionFeedback && time.Since(m.selectionFeedbackAt) > 2*time.Second {
		m.selectionFeedback = false
	}
}

func (m *Model) applySelectionHighlight(view string) string {
	if !m.selecting {
		return view
	}

	startY, startX, endY, endX := m.normalizeSelection()

	lines := strings.Split(view, "\n")

	if m.isPointSelection() {
		if startY < len(lines) {
			plain := stripANSI(lines[startY])
			if len(plain) < m.viewport.Width {
				plain += strings.Repeat(" ", m.viewport.Width-len(plain))
			}
			lines[startY] = SelectionStyle.Render(plain)
		}
		return strings.Join(lines, "\n")
	}

	for i := startY; i <= endY && i < len(lines); i++ {
		plain := stripANSI(lines[i])

		if startY == endY {
			before, selected, after := splitByColumns(plain, startX, endX)
			lines[i] = before + SelectionStyle.Render(selected) + after
		} else if i == startY {
			before, selected, _ := splitByColumns(plain, startX, len(plain)+1)
			lines[i] = before + SelectionStyle.Render(selected)
		} else if i == endY {
			selected, _, after := splitByColumns(plain, 0, endX)
			lines[i] = SelectionStyle.Render(selected) + after
		} else {
			if len(plain) < m.viewport.Width {
				plain += strings.Repeat(" ", m.viewport.Width-len(plain))
			}
			lines[i] = SelectionStyle.Render(plain)
		}
	}

	return strings.Join(lines, "\n")
}

func splitByColumns(s string, startCol, endCol int) (before, selected, after string) {
	if startCol < 0 {
		startCol = 0
	}
	if endCol < startCol {
		endCol = startCol
	}

	type clusterSpan struct {
		bytes int
		width int
	}
	var spans []clusterSpan
	totalWidth := 0
	forEachGraphemeCluster(s, func(cluster string, width int) {
		spans = append(spans, clusterSpan{bytes: len(cluster), width: width})
		totalWidth += width
	})

	if startCol > totalWidth {
		startCol = totalWidth
	}
	if endCol > totalWidth {
		endCol = totalWidth
	}

	startByteIdx := len(s)
	endByteIdx := len(s)

	col := 0
	byteIdx := 0
	startFound := startCol == 0
	endFound := endCol == 0

	if startFound {
		startByteIdx = 0
	}

	for _, sp := range spans {
		if !startFound && col >= startCol {
			startByteIdx = byteIdx
			startFound = true
		}
		if !endFound && col >= endCol {
			endByteIdx = byteIdx
			endFound = true
		}
		if startFound && endFound {
			break
		}
		col += sp.width
		byteIdx += sp.bytes
	}

	if !startFound {
		startByteIdx = len(s)
	}
	if !endFound {
		endByteIdx = len(s)
	}

	return s[:startByteIdx], s[startByteIdx:endByteIdx], s[endByteIdx:]
}
