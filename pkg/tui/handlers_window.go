package tui

import (
	tea "github.com/charmbracelet/bubbletea"
)

// handleWindowSizeMsg records the new terminal size and invalidates ALL
// render caches so content is re-wrapped at the new width. View()
// recalculates viewport dimensions on every render, so this handler only
// clears caches.
func (m *Model) handleWindowSizeMsg(msg tea.WindowSizeMsg, cmds []tea.Cmd) (tea.Model, tea.Cmd) {
	// A message carrying the size we already have is not a resize. The model
	// re-sends its own dimensions to make the renderer drop its per-row cache
	// (see consumeLayoutRepaint in frame_layout.go), and that is the only thing
	// such a message has to accomplish: dropping every render cache and
	// re-materializing the viewport here would be an O(lines) rebuild of
	// content whose inputs did not change. The geometry the viewport depends on
	// is re-checked by the render path itself (syncViewportForFrame compares
	// viewportBuiltWidth/Height against the bands View() just laid out), so a
	// band that appears or disappears is still noticed.
	if msg.Width == m.width && msg.Height == m.height && m.width != 0 {
		return m.finishUpdate(msg, cmds)
	}

	m.width = msg.Width
	m.height = msg.Height

	// Invalidate ALL render caches so content is re-wrapped to the new width.
	// View() recalculates viewport dimensions on every render, so we don't
	// need to set them here — we just need to ensure caches are cleared.
	m.streamRenderedLines = nil
	m.thinkingRenderedLines = nil
	m.streamRenderedJoined = ""
	m.thinkingRenderedJoined = ""
	m.streamRenderCacheWidth = 0
	m.thinkingRenderCacheWidth = 0
	m.renderedBaseValid = false
	m.renderedBaseKey = ""
	m.msgRenderCacheLines = nil // width changed — all rendered output is stale
	m.cachedRenderer = nil
	m.cachedRendererWidth = 0
	m.cachedRendererStyle = ""

	m.updateViewport()

	return m.finishUpdate(msg, cmds)
}
