package tui

import (
	"regexp"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// ansiEscapeRe matches ANSI escape sequences for stripping.
var ansiEscapeRe = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]|\x1b\][^\x07]*\x07|\x1b[()][0-9A-B]|\x1b[=>]`)

// stripANSI removes all ANSI escape sequences from a string.
func stripANSI(s string) string {
	return ansiEscapeRe.ReplaceAllString(s, "")
}

// SelectionStyle is the highlight applied to selected lines in the viewport.
var SelectionStyle = lipgloss.NewStyle().Background(lipgloss.Color("#264f78"))

// startSelection begins a text selection at the given screen coordinates.
// Only starts if the click is within the viewport area (left column, within viewport height).
func (m *Model) startSelection(x, y int) {
	leftWidth := int(float64(m.width) * leftColumnRatio)
	// Viewport occupies X=[0, leftWidth-2), Y=[0, m.viewport.Height)
	if x >= leftWidth-1 || y >= m.viewport.Height {
		return
	}
	m.selecting = true
	m.selStartY = y
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
	m.selEndY = y
}

// finishSelection completes the selection, extracts text, copies to clipboard.
func (m *Model) finishSelection() {
	if !m.selecting {
		return
	}
	m.selecting = false

	// Determine line range (normalize start/end)
	startLine := m.selStartY
	endLine := m.selEndY
	if startLine > endLine {
		startLine, endLine = endLine, startLine
	}

	// Extract text from viewport content
	text := m.extractSelectionText(startLine, endLine)
	if text == "" {
		return
	}

	copyToClipboard(text)
	m.selectionFeedback = true
	m.selectionFeedbackAt = time.Now()
}

// extractSelectionText gets the plain text (ANSI-stripped) for the selected
// line range. Lines are relative to the viewport's current scroll position.
func (m *Model) extractSelectionText(startLine, endLine int) string {
	// Get the full viewport content and split into lines
	content := m.viewport.View()
	visibleLines := strings.Split(content, "\n")

	if startLine >= len(visibleLines) {
		return ""
	}
	if endLine >= len(visibleLines) {
		endLine = len(visibleLines) - 1
	}

	var sb strings.Builder
	for i := startLine; i <= endLine; i++ {
		plain := stripANSI(visibleLines[i])
		// Trim trailing whitespace but preserve leading (indentation matters)
		plain = strings.TrimRight(plain, " ")
		sb.WriteString(plain)
		if i < endLine {
			sb.WriteString("\n")
		}
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
// a background highlight to the selected lines. Returns the modified string.
func (m *Model) applySelectionHighlight(view string) string {
	if !m.selecting {
		return view
	}

	startLine := m.selStartY
	endLine := m.selEndY
	if startLine > endLine {
		startLine, endLine = endLine, startLine
	}

	lines := strings.Split(view, "\n")
	for i := startLine; i <= endLine && i < len(lines); i++ {
		// Apply selection background to the full line width
		plain := stripANSI(lines[i])
		// Pad to viewport width for consistent highlight
		if len(plain) < m.viewport.Width {
			plain += strings.Repeat(" ", m.viewport.Width-len(plain))
		}
		lines[i] = SelectionStyle.Render(plain)
	}

	return strings.Join(lines, "\n")
}
