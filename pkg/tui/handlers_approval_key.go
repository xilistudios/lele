package tui

import (
	tea "github.com/charmbracelet/bubbletea"
)

func (m *Model) handleApprovalKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y":
		m.handleApproval(true)
		return m, nil
	case "n", "N", "esc":
		m.handleApproval(false)
		return m, nil
	case "v", "V":
		m.approvalShowFull = !m.approvalShowFull
		m.updateViewport()
		return m, nil
	case "w", "W":
		m.handleWhitelistApproval()
		return m, nil
	case "up", "down", "pgup", "pgdown":
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		if msg.String() == "up" || msg.String() == "pgup" {
			m.maybeExpandRenderWindow()
		}
		return m, cmd
	case "ctrl+c":
		m.cancel()
		return m, tea.Quit
	}
	return m, nil

}
