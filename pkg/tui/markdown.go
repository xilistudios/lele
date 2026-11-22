package tui

import (
	"strings"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

func (m *Model) getMarkdownRenderer(width int) *glamour.TermRenderer {
	style := m.glamourStyleName()
	if m.cachedRenderer != nil && m.cachedRendererWidth == width && m.cachedRendererStyle == style {
		return m.cachedRenderer
	}
	renderer, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle(style),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		return nil
	}
	m.cachedRenderer = renderer
	m.cachedRendererWidth = width
	m.cachedRendererStyle = style
	return renderer
}

func (m *Model) glamourStyleName() string {
	if m.themeIsLight {
		return "light"
	}
	return "dark"
}

func clampMarkdownWidth(rendered string, width int) string {
	if width <= 0 || rendered == "" {
		return rendered
	}
	lines := strings.Split(rendered, "\n")
	for i, line := range lines {
		if ansi.StringWidth(line) > width {
			lines[i] = closeOpenSGR(ansi.Truncate(line, width, ""))
		}
	}
	return strings.Join(lines, "\n")
}

func (m *Model) renderMarkdown(content string, width int) string {
	content = sanitizeDisplayText(content)
	renderer := m.getMarkdownRenderer(width)
	if renderer != nil {
		rendered, err := renderer.Render(content)
		if err == nil {
			return clampMarkdownWidth(strings.TrimSuffix(rendered, "\n"), width)
		}
	}

	return simpleMarkdownRender(content, width)
}

func simpleMarkdownRender(content string, width int) string {
	lines := strings.Split(content, "\n")
	var result strings.Builder

	headerStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(AccentColor)

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

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
			if width > 0 && ansi.StringWidth(line) > width {
				result.WriteString(wrapText(line, width) + "\n")
			} else {
				result.WriteString(line + "\n")
			}
		}
	}

	return strings.TrimSuffix(result.String(), "\n")
}

func (m *Model) getRenderedStream(width int) string {
	rawLines := strings.Split(m.currentStream, "\n")
	if len(rawLines) == 0 {
		return ""
	}

	if m.streamRenderCacheWidth != width {
		m.streamRenderedLines = nil
		m.streamRenderedJoined = ""
		m.streamRenderCacheWidth = width
	}

	if len(m.streamRenderedLines) == 0 {
		m.streamRenderedLines = make([]string, 0, len(rawLines))
		m.streamRenderedJoined = ""
	}

	for len(m.streamRenderedLines) < len(rawLines)-1 {
		idx := len(m.streamRenderedLines)
		renderedLine := mergeAdjacentSGR(renderSingleLine(rawLines[idx], width))
		m.streamRenderedLines = append(m.streamRenderedLines, renderedLine)
		if m.streamRenderedJoined == "" {
			m.streamRenderedJoined = renderedLine
		} else {
			m.streamRenderedJoined += "\n" + renderedLine
		}
	}

	lastLine := rawLines[len(rawLines)-1]
	renderedLastLine := mergeAdjacentSGR(renderSingleLine(lastLine, width))

	if m.streamRenderedJoined == "" {
		return renderedLastLine
	}
	return m.streamRenderedJoined + "\n" + renderedLastLine
}

func (m *Model) getRenderedThinking(width int) string {
	rawLines := strings.Split(m.currentThinking, "\n")
	if len(rawLines) == 0 {
		return ""
	}

	if m.thinkingRenderCacheWidth != width {
		m.thinkingRenderedLines = nil
		m.thinkingRenderedJoined = ""
		m.thinkingRenderCacheWidth = width
	}

	if len(m.thinkingRenderedLines) == 0 {
		m.thinkingRenderedLines = make([]string, 0, len(rawLines))
		m.thinkingRenderedJoined = ""
	}

	for len(m.thinkingRenderedLines) < len(rawLines)-1 {
		idx := len(m.thinkingRenderedLines)
		renderedLine := mergeAdjacentSGR(renderSingleLine(rawLines[idx], width))
		m.thinkingRenderedLines = append(m.thinkingRenderedLines, renderedLine)
		if m.thinkingRenderedJoined == "" {
			m.thinkingRenderedJoined = renderedLine
		} else {
			m.thinkingRenderedJoined += "\n" + renderedLine
		}
	}

	lastLine := rawLines[len(rawLines)-1]
	renderedLastLine := mergeAdjacentSGR(renderSingleLine(lastLine, width))

	if m.thinkingRenderedJoined == "" {
		return renderedLastLine
	}
	return m.thinkingRenderedJoined + "\n" + renderedLastLine
}

func renderSingleLine(line string, width int) string {
	line = sanitizeDisplayText(line)
	trimmed := strings.TrimSpace(line)
	headerStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(AccentColor)

	if strings.HasPrefix(trimmed, "# ") {
		text := strings.TrimPrefix(trimmed, "# ")
		return clampMarkdownWidth(headerStyle.Render(text), width) + "\n"
	} else if strings.HasPrefix(trimmed, "## ") {
		text := strings.TrimPrefix(trimmed, "## ")
		return clampMarkdownWidth(headerStyle.Render(text), width) + "\n"
	} else if strings.HasPrefix(trimmed, "### ") {
		text := strings.TrimPrefix(trimmed, "### ")
		return clampMarkdownWidth(headerStyle.Render(text), width) + "\n"
	}

	if width > 0 && ansi.StringWidth(line) > width {
		return wrapText(line, width)
	}
	return line
}
