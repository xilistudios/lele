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

// getRenderedStream wraps and returns the current stream text line-by-line, utilizing a line cache for speed.
func (m *Model) getRenderedStream(width int) string {
	rawLines := strings.Split(m.currentStream, "\n")
	if len(rawLines) == 0 {
		return ""
	}

	if len(m.streamRenderedLines) == 0 {
		m.streamRenderedLines = make([]string, 0, len(rawLines))
	}

	for len(m.streamRenderedLines) < len(rawLines)-1 {
		idx := len(m.streamRenderedLines)
		renderedLine := renderSingleLine(rawLines[idx], width)
		m.streamRenderedLines = append(m.streamRenderedLines, renderedLine)
	}

	lastLine := rawLines[len(rawLines)-1]
	renderedLastLine := renderSingleLine(lastLine, width)

	if len(m.streamRenderedLines) == 0 {
		return renderedLastLine
	}
	return strings.Join(m.streamRenderedLines, "\n") + "\n" + renderedLastLine
}

// getRenderedThinking wraps and returns the thinking stream text line-by-line, utilizing a line cache for speed.
func (m *Model) getRenderedThinking(width int) string {
	rawLines := strings.Split(m.currentThinking, "\n")
	if len(rawLines) == 0 {
		return ""
	}

	if len(m.thinkingRenderedLines) == 0 {
		m.thinkingRenderedLines = make([]string, 0, len(rawLines))
	}

	for len(m.thinkingRenderedLines) < len(rawLines)-1 {
		idx := len(m.thinkingRenderedLines)
		renderedLine := renderSingleLine(rawLines[idx], width)
		m.thinkingRenderedLines = append(m.thinkingRenderedLines, renderedLine)
	}

	lastLine := rawLines[len(rawLines)-1]
	renderedLastLine := renderSingleLine(lastLine, width)

	if len(m.thinkingRenderedLines) == 0 {
		return renderedLastLine
	}
	return strings.Join(m.thinkingRenderedLines, "\n") + "\n" + renderedLastLine
}

func renderSingleLine(line string, width int) string {
	// NOTE: line-by-line rendering during streaming does not preserve multi-line
	// block context (tables, code fences); the final full render uses renderMarkdown.
	trimmed := strings.TrimSpace(line)
	headerStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(AccentColor)

	if strings.HasPrefix(trimmed, "# ") {
		text := strings.TrimPrefix(trimmed, "# ")
		return headerStyle.Render(text) + "\n"
	} else if strings.HasPrefix(trimmed, "## ") {
		text := strings.TrimPrefix(trimmed, "## ")
		return headerStyle.Render(text) + "\n"
	} else if strings.HasPrefix(trimmed, "### ") {
		text := strings.TrimPrefix(trimmed, "### ")
		return headerStyle.Render(text) + "\n"
	}

	if width > 0 && len(line) > width {
		return wrapText(line, width)
	}
	return line
}
