package tui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// handleAsyncResult processes the small result/event messages: compaction
// feedback, skill scan/install/toggle/delete results, onboarding verify and
// the stream-render throttle tick. Cases that do not return early keep the
// original fall-through into update()'s post-switch block.
func (m *Model) handleAsyncResult(msg tea.Msg, cmds []tea.Cmd) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case compactResultMsg:
		if msg.sessionKey == m.currentKey {
			m.lastDuration = time.Since(m.startTime)
			m.processing = false
			m.currentToolAction = ""
			m.compactFeedback = msg.result
			m.forceGotoBottom = true
			m.reloadSessions()
			// Compaction rewrites (or excludes) the session history, so every
			// per-message render-cache entry keyed by a pre-compact fingerprint
			// is unreachable. The next rebuild prunes the cache to the live
			// window anyway (see buildRenderedHistoryLines); dropping it here
			// is an O(1) belt-and-braces so stale entries never survive even
			// if the rebuild path is skipped. Unconditional: a failed
			// compaction only costs one re-render (hallazgo P5).
			m.msgRenderCacheLines = nil
		}

	case skillsScanResultMsg:
		return m, m.handleSkillScanResult(msg)

	case skillsInstallResultMsg:
		return m, m.handleSkillInstallResult(msg)

	case skillToggleResultMsg:
		return m, m.handleSkillToggleResult(msg)

	case skillDeleteResultMsg:
		return m, m.handleSkillDeleteResult(msg)

	case obVerifyResultMsg:
		m.obVerifying = false
		if !msg.success {
			m.obVerifyFailed = true
		}
		m.onboardingStep = obDone
		m.obFinalizeSetup()
		return m, nil

	case streamThrottleMsg:
		m.streamThrottleActive = false
		if m.streamPendingUpdate {
			m.updateViewport()
			m.streamPendingUpdate = false
			m.streamThrottleActive = true
			cmds = append(cmds, tea.Tick(m.streamThrottleInterval, func(t time.Time) tea.Msg {
				return streamThrottleMsg{}
			}))
		}
	}

	return m.finishUpdate(msg, cmds)
}
