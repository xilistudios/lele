package tui

import (
	"strings"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
)

// getMarkdownRenderer returns a cached glamour.TermRenderer for the given width.
// A new renderer is created only when the width changes.
func (m *Model) getMarkdownRenderer(width int) *glamour.TermRenderer {
	if m.cachedRenderer != nil && m.cachedRendererWidth == width {
		return m.cachedRenderer
	}
	renderer, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle("dark"),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		return nil
	}
	m.cachedRenderer = renderer
	m.cachedRendererWidth = width
	return renderer
}

// renderMarkdown renders markdown content for terminal display.
// Uses glamour for full markdown rendering with fallback to simple header rendering.
func (m *Model) renderMarkdown(content string, width int) string {
	renderer := m.getMarkdownRenderer(width)
	if renderer != nil {
		rendered, err := renderer.Render(content)
		if err == nil {
			return strings.TrimSuffix(rendered, "\n")
		}
	}

	// Fallback: simple manual rendering for headers and basic formatting
	return simpleMarkdownRender(content, width)
}

// simpleMarkdownRender provides basic markdown rendering without glamour.
func simpleMarkdownRender(content string, width int) string {
	lines := strings.Split(content, "\n")
	var result strings.Builder

	headerStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("39")) // Blue color for headers

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Headers
		if strings.HasPrefix(trimmed, "# ") {
			text := strings.TrimPrefix(trimmed, "# ")
			result.WriteString(headerStyle.Render(text) + "\n\n")
		} else if strings.HasPrefix(trimmed, "## ") {
			text := strings.TrimPrefix(trimmed, "## ")
			result.WriteString(headerStyle.Render(text) + "\n\n")
		} else if strings.HasPrefix(trimmed, "### ") {
			text := strings.TrimPrefix(trimmed, "### ")
			result.WriteString(headerStyle.Render(text) + "\n\n")
		} else {
			// Regular text - wrap if needed
			if width > 0 && len(line) > width {
				result.WriteString(wrapText(line, width) + "\n")
			} else {
				result.WriteString(line + "\n")
			}
		}
	}

	return strings.TrimSuffix(result.String(), "\n")
}
