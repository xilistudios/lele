package tui

import (
	tea "github.com/charmbracelet/bubbletea"
)

type frameLayout struct {
	width            int
	height           int
	viewportHeight   int
	queueRows        int
	autocompleteRows int
	welcome          bool
	modal            modalType
}

func (m *Model) noteFrameLayout(width, height, viewportHeight, queueRows, autocompleteRows int) {
	m.frameLayout = frameLayout{
		width:            width,
		height:           height,
		viewportHeight:   viewportHeight,
		queueRows:        queueRows,
		autocompleteRows: autocompleteRows,
		welcome:          m.showWelcome,
		modal:            m.modalMode,
	}
}

func (m *Model) consumeLayoutRepaint() tea.Cmd {
	if m.frameLayout.width == 0 || m.frameLayout.height == 0 {
		return nil
	}
	if m.frameLayoutRepainted && m.frameLayout == m.lastPaintedLayout {
		return nil
	}
	m.lastPaintedLayout = m.frameLayout
	m.frameLayoutRepainted = true

	width, height := m.width, m.height
	return func() tea.Msg {
		return tea.WindowSizeMsg{Width: width, Height: height}
	}
}
