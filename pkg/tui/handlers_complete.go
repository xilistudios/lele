package tui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// handleCompleteMsg finalises a turn for the visible session: clears loading
// state, records duration and reconciles queued subagent completions.
func (m *Model) handleCompleteMsg(msg completeMsg, cmds []tea.Cmd) (tea.Model, tea.Cmd) {
	if msg.sessionKey == m.currentKey {
		if m.pendingSubagentCompletions > 0 {
			if !m.parentCompletionObserved {
				// This is the completion of the original parent turn. The
				// queued subagent result will start another parent turn.
				m.parentCompletionObserved = true
				m.reloadSessions()
				return m.finishUpdate(msg, cmds) // parent turn done, subagent continuation queued
			}
			m.pendingSubagentCompletions--
			if m.pendingSubagentCompletions > 0 {
				m.reloadSessions()
				return m.finishUpdate(msg, cmds) // more subagent results queued
			}
		}
		m.parentCompletionObserved = true
		m.lastDuration = time.Since(m.startTime)
		m.clearStreamingState()
		// clearStreamingState preserves an active backend session when
		// navigating between chats. This completion belongs to the current
		// turn, so its local loading state must be cleared explicitly.
		m.processing = false
		m.reloadSessions()

		// Schedule a follow-up tick so the view re-renders once the
		// backend's deferred session-cancel cleanup runs. Without this
		// tick the loading indicator can stay stuck because
		// isSessionProcessing() still returns true via the backend check
		// even after m.processing is false, and no event triggers a
		// re-render to detect the backend state change.
		if m.isSessionProcessing() {
			cmds = append(cmds, m.tickCmd())
		}
	}

	return m.finishUpdate(msg, cmds)
}
