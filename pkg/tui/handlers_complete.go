package tui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func (m *Model) handleCompleteMsg(msg completeMsg, cmds []tea.Cmd) (tea.Model, tea.Cmd) {
	if msg.sessionKey == m.currentKey {
		m.subagentContinuationPending = false
		if !m.startTime.IsZero() {
			m.lastDuration = time.Since(m.startTime)
		}
		finalTokPerSec := m.streamTokPerSec
		m.clearStreamingState()
		m.lastTokPerSec = finalTokPerSec
		m.processing = false
		m.reloadSessions()

		if m.isSessionProcessing() {
			cmds = append(cmds, m.tickCmd())
		}
	}

	return m.finishUpdate(msg, cmds)
}
