package tui

import (
	tea "github.com/charmbracelet/bubbletea"
)

// handleWindowSizeMsg records the new terminal size and invalidates ALL
// render caches so content is re-wrapped at the new width. View()
// recalculates viewport dimensions on every render, so this handler only
// clears caches.
func (m *Model) handleWindowSizeMsg(msg tea.WindowSizeMsg, cmds []tea.Cmd) (tea.Model, tea.Cmd) {
	m.width = msg.Width
	m.height = msg.Height

	// Invalidate ALL render caches so content is re-wrapped to the new width.
	// View() recalculates viewport dimensions on every render, so we don't
	// need to set them here — we just need to ensure caches are cleared.
	m.streamRenderedLines = nil
	m.thinkingRenderedLines = nil
	m.streamRenderedJoined = ""
	m.thinkingRenderedJoined = ""
	m.renderedBaseValid = false
	m.renderedBaseKey = ""
	m.msgRenderCacheLines = nil // width changed — all rendered output is stale
	m.cachedRenderer = nil
	m.cachedRendererWidth = 0

	m.updateViewport()

	return m.finishUpdate(msg, cmds)
}
