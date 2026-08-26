package tui

import (
	"regexp"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

// ansiEscapeRe matches ANSI escape sequences for stripping.
var ansiEscapeRe = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]|\x1b\][^\x07]*\x07|\x1b[()][0-9A-B]|\x1b[=>]`)

// stripANSI removes all ANSI escape sequences from a string.
func stripANSI(s string) string {
	return ansiEscapeRe.ReplaceAllString(s, "")
}

// SelectionStyle is the highlight applied to selected text in the viewport.
// It is populated by rebuildStyles() in style.go.
var SelectionStyle lipgloss.Style

// startSelection begins a text selection at the given screen coordinates.
// Only starts if the click is within the viewport area (left column, within viewport height).
func (m *Model) startSelection(x, y int) {
	leftWidth := int(float64(m.width) * leftColumnRatio)
	// Viewport occupies X=[0, leftWidth-2), Y=[0, m.viewport.Height)
	if x >= leftWidth-1 || y >= m.viewport.Height {
		return
	}
	m.selecting = true
	m.selStartX = x
	m.selStartY = y
	m.selEndX = x
	m.selEndY = y
}

// updateSelection updates the selection end point during a drag.
func (m *Model) updateSelection(x, y int) {
	if !m.selecting {
		return
	}
	// Clamp to viewport bounds
	if y < 0 {
		y = 0
	}
	if y >= m.viewport.Height {
		y = m.viewport.Height - 1
	}
	if x < 0 {
		x = 0
	}
	if x >= m.viewport.Width {
		x = m.viewport.Width
	}
	m.selEndX = x
	m.selEndY = y
}

// normalizeSelection returns the selection coordinates in top-left to
// bottom-right order: (startY, startX, endY, endX).
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

// isPointSelection returns true if the selection is a single point (click
// without meaningful drag). Used for backward-compatible full-line copy.
func (m *Model) isPointSelection() bool {
	return m.selStartX == m.selEndX && m.selStartY == m.selEndY
}

// finishSelection completes the selection, extracts text, copies to clipboard.
func (m *Model) finishSelection() {
	if !m.selecting {
		return
	}
	m.selecting = false

	startY, startX, endY, endX := m.normalizeSelection()

	// Extract text from viewport content
	text := m.extractSelectionText(startY, startX, endY, endX)
	if text == "" {
		return
	}

	copyToClipboard(text)
	m.selectionFeedback = true
	m.selectionFeedbackAt = time.Now()
}

// extractSelectionText gets the plain text (ANSI-stripped) for the selected
// range. Coordinates are normalized (startY <= endY). If the selection is a
// single point (click without drag), the full clicked line is returned for
// backward compatibility.
func (m *Model) extractSelectionText(startY, startX, endY, endX int) string {
	content := m.viewport.View()
	visibleLines := strings.Split(content, "\n")

	if startY >= len(visibleLines) {
		return ""
	}
	if endY >= len(visibleLines) {
		endY = len(visibleLines) - 1
	}

	// Backward compat: single click copies the full line
	if m.isPointSelection() {
		plain := stripANSI(visibleLines[startY])
		return strings.TrimRight(plain, " ")
	}

	var sb strings.Builder

	if startY == endY {
		// Single line: extract column range
		plain := stripANSI(visibleLines[startY])
		_, selected, _ := splitByColumns(plain, startX, endX)
		sb.WriteString(strings.TrimRight(selected, " "))
	} else {
		// First line: from startX to end of line
		firstPlain := stripANSI(visibleLines[startY])
		_, firstSelected, _ := splitByColumns(firstPlain, startX, len(firstPlain)+1)
		sb.WriteString(strings.TrimRight(firstSelected, " "))
		sb.WriteString("\n")

		// Middle lines: full content
		for i := startY + 1; i < endY; i++ {
			plain := stripANSI(visibleLines[i])
			sb.WriteString(strings.TrimRight(plain, " "))
			sb.WriteString("\n")
		}

		// Last line: from beginning to endX
		lastPlain := stripANSI(visibleLines[endY])
		lastSelected, _, _ := splitByColumns(lastPlain, 0, endX)
		sb.WriteString(strings.TrimRight(lastSelected, " "))
	}

	return strings.TrimRight(sb.String(), "\n")
}

// clearSelectionFeedback clears the "Copied!" feedback after a timeout.
// Called from the tick handler.
func (m *Model) clearSelectionFeedback() {
	if m.selectionFeedback && time.Since(m.selectionFeedbackAt) > 2*time.Second {
		m.selectionFeedback = false
	}
}

// applySelectionHighlight takes the viewport's rendered view string and applies
// a background highlight to the selected character range. Returns the modified string.
func (m *Model) applySelectionHighlight(view string) string {
	if !m.selecting {
		return view
	}

	startY, startX, endY, endX := m.normalizeSelection()

	lines := strings.Split(view, "\n")

	// Backward compat: single click highlights the full line
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
			// Single line: highlight only [startX, endX)
			before, selected, after := splitByColumns(plain, startX, endX)
			lines[i] = before + SelectionStyle.Render(selected) + after
		} else if i == startY {
			// First line: highlight from startX to end
			before, selected, _ := splitByColumns(plain, startX, len(plain)+1)
			lines[i] = before + SelectionStyle.Render(selected)
		} else if i == endY {
			// Last line: highlight from beginning to endX
			selected, _, after := splitByColumns(plain, 0, endX)
			lines[i] = SelectionStyle.Render(selected) + after
		} else {
			// Middle line: highlight full line
			if len(plain) < m.viewport.Width {
				plain += strings.Repeat(" ", m.viewport.Width-len(plain))
			}
			lines[i] = SelectionStyle.Render(plain)
		}
	}

	return strings.Join(lines, "\n")
}

// splitByColumns splits a plain-text string s into three parts at terminal
// column boundaries: [0, startCol), [startCol, endCol), [endCol, ...).
// It correctly handles wide characters (CJK, emoji) that occupy 2 columns.
// Column values are clamped to the string's actual width.
func splitByColumns(s string, startCol, endCol int) (before, selected, after string) {
	if startCol < 0 {
		startCol = 0
	}
	if endCol < startCol {
		endCol = startCol
	}

	runes := []rune(s)
	widths := make([]int, len(runes))
	totalWidth := 0
	for i, r := range runes {
		w := runewidth.RuneWidth(r)
		widths[i] = w
		totalWidth += w
	}

	// Clamp to actual width
	if startCol > totalWidth {
		startCol = totalWidth
	}
	if endCol > totalWidth {
		endCol = totalWidth
	}

	// Find byte indices for column boundaries
	startByteIdx := len(s) // default: past end
	endByteIdx := len(s)

	col := 0
	byteIdx := 0
	startFound := startCol == 0
	endFound := endCol == 0

	if startFound {
		startByteIdx = 0
	}

	for i, r := range runes {
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
		col += widths[i]
		byteIdx += len(string(r))
		_ = i
	}

	// Handle boundaries at exact end
	if !startFound {
		startByteIdx = len(s)
	}
	if !endFound {
		endByteIdx = len(s)
	}

	return s[:startByteIdx], s[startByteIdx:endByteIdx], s[endByteIdx:]
}
