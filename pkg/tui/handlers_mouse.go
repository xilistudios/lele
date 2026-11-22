package tui

import (
	tea "github.com/charmbracelet/bubbletea"
)

func (m *Model) handleMouseMsg(msg tea.MouseMsg, cmds []tea.Cmd) (tea.Model, tea.Cmd) {
	if !m.mouseEnabled {
		return m.finishUpdate(msg, cmds)
	}
	if msg.Button == tea.MouseButtonWheelUp || msg.Button == tea.MouseButtonWheelDown {
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		if msg.Button == tea.MouseButtonWheelUp {
			m.maybeExpandRenderWindow()
		}
		return m, cmd
	}

	if msg.Button == tea.MouseButtonLeft {
		leftWidth := int(float64(m.width) * leftColumnRatio)
		inViewportArea := msg.X < leftWidth-1 && msg.Y < m.viewport.Height

		switch msg.Action {
		case tea.MouseActionPress:
			if inViewportArea && m.modalMode == ModalNone {
				m.startSelection(msg.X, msg.Y)
				return m, nil
			}
		case tea.MouseActionMotion:
			if m.selecting {
				m.updateSelection(msg.X, msg.Y)
				return m, nil
			}
		case tea.MouseActionRelease:
			if m.selecting {
				m.finishSelection()
				return m, nil
			}
		}
	}

	if m.modalMode == ModalNone && msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
		leftWidth := int(float64(m.width) * leftColumnRatio)

		if m.hasBackToParentClick && m.parentSessionKey != "" &&
			msg.X < leftWidth-1 &&
			msg.Y >= m.backToParentClick.yStart && msg.Y < m.backToParentClick.yEnd {
			m.setCurrentChatKey(m.parentSessionKey)
			m.parentSessionKey = ""
			m.clearStreamingState()
			m.reloadSessions()
			m.hasBackToParentClick = false
			if m.isSessionProcessing() {
				return m, m.tickCmd()
			}
			return m, nil
		}

		rightWidth := m.width - leftWidth - chatSidebarGutter - 3
		if rightWidth < 1 {
			rightWidth = 1
		}
		sidebarStartX := leftWidth + chatSidebarGutter

		if msg.X >= sidebarStartX && msg.X < sidebarStartX+rightWidth+1 {
			for _, target := range m.subagentClickTargets {
				if msg.Y >= target.yStart && msg.Y < target.yEnd {
					if m.parentSessionKey == "" {
						m.parentSessionKey = m.currentKey
					}
					m.setCurrentChatKey(target.key)
					m.showWelcome = false
					m.clearStreamingState()
					m.reloadSessions()
					if m.isSessionProcessing() {
						return m, m.tickCmd()
					}
					return m, nil
				}
			}
		}
	}

	return m.finishUpdate(msg, cmds)
}
