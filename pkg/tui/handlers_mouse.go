package tui

import (
	tea "github.com/charmbracelet/bubbletea"
)

// handleMouseMsg processes wheel scrolling on the viewport, click-drag text
// selection and sidebar subagent clicks. Mouse events no branch consumed
// still run update()'s post-switch block via finishUpdate: the textarea skips
// them, but slash-command autocomplete state is refreshed exactly as before.
func (m *Model) handleMouseMsg(msg tea.MouseMsg, cmds []tea.Cmd) (tea.Model, tea.Cmd) {
	if !m.mouseEnabled {
		return m.finishUpdate(msg, cmds) // mouse capture off
	}
	// Handle mouse scrolling on the viewport
	if msg.Button == tea.MouseButtonWheelUp || msg.Button == tea.MouseButtonWheelDown {
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		if msg.Button == tea.MouseButtonWheelUp {
			m.maybeExpandRenderWindow()
		}
		return m, cmd
	}

	// Handle text selection via click+drag in the viewport area
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

	// Handle mouse clicks on subagent items in the sidebar (only if no modal is active)
	if m.modalMode == ModalNone && msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
		// Check if click is in the right sidebar area
		leftWidth := int(float64(m.width) * leftColumnRatio)
		rightWidth := m.width - leftWidth - 3
		sidebarStartX := leftWidth + 1 // +1 for separator

		if msg.X >= sidebarStartX && msg.X < sidebarStartX+rightWidth {
			// Check if click matches any subagent click target Y-coordinate in the sidebar text
			for _, target := range m.subagentClickTargets {
				if msg.Y >= target.yStart && msg.Y < target.yEnd {
					// Clicked on a subagent item - switch to that subagent's chat
					if m.parentSessionKey == "" {
						m.parentSessionKey = m.currentKey
					}
					m.setCurrentChatKey(target.key)
					m.showWelcome = false
					m.clearStreamingState()
					m.reloadSessions()
					// Restart tick animation if the target subagent is actively processing.
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
