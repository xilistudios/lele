package tui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// handleCompleteMsg finalises a turn for the visible session: it clears the
// loading state, records the turn duration and reconciles the subagent
// continuation flag. It is intentionally unconditional and idempotent — a
// backend-initiated continuation turn carries no m.currentMessageID, so the
// client cannot tell it apart from the parent turn by identity, and any rule
// that tried to would deadlock (the backend still holds the session semaphore
// when the stream-end is published). Clearing already-clear state is harmless,
// and the sessionKey guard keeps a completion for another session from
// touching this one.
func (m *Model) handleCompleteMsg(msg completeMsg, cmds []tea.Cmd) (tea.Model, tea.Cmd) {
	if msg.sessionKey == m.currentKey {
		// The turn this completion belongs to is over, so no continuation is
		// owed any more — whether it was the parent turn or the continuation.
		m.subagentContinuationPending = false
		if !m.startTime.IsZero() {
			m.lastDuration = time.Since(m.startTime)
		}
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
