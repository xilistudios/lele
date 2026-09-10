package tui

import (
	tea "github.com/charmbracelet/bubbletea"
)

// handleApprovalKey processes a keystroke while a command approval is pending.
// The agent is blocked waiting for the user's decision, so these keys take
// priority over typing new messages; scrolling is still allowed to review
// context and every other key is swallowed. The block is terminal.
func (m *Model) handleApprovalKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y":
		m.handleApproval(true)
		return m, nil
	case "n", "N", "esc":
		m.handleApproval(false)
		return m, nil
	case "v", "V":
		// Toggle between the one-line preview and the full command.
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
		// Never trap quit during an approval prompt — a wedged terminal
		// would otherwise require SIGINT.
		m.cancel()
		return m, tea.Quit
	}
	// Block all other input while approval is pending
	return m, nil

}
