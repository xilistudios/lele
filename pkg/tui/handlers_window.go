package tui

import (
	tea "github.com/charmbracelet/bubbletea"
)

func (m *Model) handleWindowSizeMsg(msg tea.WindowSizeMsg, cmds []tea.Cmd) (tea.Model, tea.Cmd) {
	if msg.Width == m.width && msg.Height == m.height && m.width != 0 {
		return m.finishUpdate(msg, cmds)
	}

	m.width = msg.Width
	m.height = msg.Height

	m.streamRenderedLines = nil
	m.thinkingRenderedLines = nil
	m.streamRenderedJoined = ""
	m.thinkingRenderedJoined = ""
	m.streamRenderCacheWidth = 0
	m.thinkingRenderCacheWidth = 0
	m.renderedBaseValid = false
	m.renderedBaseKey = ""
	m.msgRenderCacheLines = nil
	m.cachedRenderer = nil
	m.cachedRendererWidth = 0
	m.cachedRendererStyle = ""

	m.updateViewport()

	return m.finishUpdate(msg, cmds)
}
