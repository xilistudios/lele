package tui

import (
	"strings"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// getMarkdownRenderer returns a cached glamour.TermRenderer for the given width.
// A new renderer is created only when the width or the light/dark mode changes.
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

// glamourStyleName picks "light" or "dark" based on the active theme
// background luminance, so light themes do not render dark-themed markdown.
func (m *Model) glamourStyleName() string {
	if m.themeIsLight {
		return "light"
	}
	return "dark"
}

// clampMarkdownWidth truncates every rendered line to at most width display
// cells. glamour's WithWordWrap does NOT cut unbreakable tokens (filesystem
// paths, URLs, hashes) — a single line can come back 10+ cells over the wrap
// budget. That overflow paints over the sidebar border on scroll (Terminal.app
// shows it as a short Accent-colored bar).
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

// renderMarkdown renders markdown content for terminal display.
// Uses glamour for full markdown rendering with fallback to simple header rendering.
func (m *Model) renderMarkdown(content string, width int) string {
	content = sanitizeDisplayText(content)
	renderer := m.getMarkdownRenderer(width)
	if renderer != nil {
		rendered, err := renderer.Render(content)
		if err == nil {
			return clampMarkdownWidth(strings.TrimSuffix(rendered, "\n"), width)
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
		Foreground(AccentColor)

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
			// Regular text - wrap if needed. Compare the visual cell width
			// (not the byte length) so multi-byte characters (CJK, emoji,
			// accents) don't trigger premature wrapping.
			if width > 0 && ansi.StringWidth(line) > width {
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

	// Invalidate the line cache when the terminal width changed: cached
	// lines were wrapped for the old width and would render incorrectly.
	if m.streamRenderCacheWidth != width {
		m.streamRenderedLines = nil
		m.streamRenderedJoined = ""
		m.streamRenderCacheWidth = width
	}

	if len(m.streamRenderedLines) == 0 {
		m.streamRenderedLines = make([]string, 0, len(rawLines))
		m.streamRenderedJoined = ""
	}

	// Render newly completed lines and append them to the accumulated joined
	// string in O(1), avoiding an O(n²) strings.Join over all cached lines on
	// every streaming chunk.
	for len(m.streamRenderedLines) < len(rawLines)-1 {
		idx := len(m.streamRenderedLines)
		// mergeAdjacentSGR collapses the per-token SGR churn glamour/chroma
		// emit (cell-identical, ~8x fewer bytes); done here because these
		// lines are cached and re-read on every streaming frame.
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

// getRenderedThinking wraps and returns the thinking stream text line-by-line, utilizing a line cache for speed.
func (m *Model) getRenderedThinking(width int) string {
	rawLines := strings.Split(m.currentThinking, "\n")
	if len(rawLines) == 0 {
		return ""
	}

	// Invalidate the line cache when the terminal width changed: cached
	// lines were wrapped for the old width and would render incorrectly.
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
		// mergeAdjacentSGR collapses the per-token SGR churn glamour/chroma
		// emit (cell-identical, ~8x fewer bytes); done here because these
		// lines are cached and re-read on every streaming frame.
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
	// NOTE: line-by-line rendering during streaming does not preserve multi-line
	// block context (tables, code fences); the final full render uses renderMarkdown.
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
